package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

const runnerName = "unreal-agent-runner"

// resolveRunner finds the runner: explicit path (--runner / $UAGENT_RUNNER), ~/.local/bin, then PATH.
func resolveRunner(explicit string) (string, error) {
	if explicit != "" {
		if !isExecutable(explicit) {
			return "", fmt.Errorf("runner %q is not an executable file", explicit)
		}
		return explicit, nil
	}
	if home, err := os.UserHomeDir(); err == nil {
		if p := filepath.Join(home, ".local", "bin", runnerName); isExecutable(p) {
			return p, nil
		}
	}
	if p, err := exec.LookPath(runnerName); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("%s not found; install with:\n  GOBIN=\"$HOME/.local/bin\" go install github.com/unreallabsai/unreal-agent/cmd/unreal-agent-runner@latest", runnerName)
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0
}

// checkDotEnv guards against issue #5: the runner loads <workspace>/.env and
// lets it redirect the LLM endpoint or credentials. It returns the risky keys found.
func checkDotEnv(workspace string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(workspace, ".env"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read workspace .env: %w", err)
	}
	var risky []string
	for line := range strings.Lines(string(data)) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, _, ok := strings.Cut(line, "=")
		name = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(name), "export "))
		if ok && isRiskyEnv(name) && !slices.Contains(risky, name) {
			risky = append(risky, name)
		}
	}
	return risky, nil
}

func isRiskyEnv(name string) bool {
	upper := strings.ToUpper(name)
	return strings.HasPrefix(upper, "UNREAL_HARNESS_") ||
		strings.HasPrefix(upper, "OPENAI_CODEX_") ||
		upper == "CODEX_HOME" ||
		upper == "SANDBOX_EGRESS_PROXY" ||
		strings.HasSuffix(upper, "_PROXY")
}

// checkAuth verifies the provider has credentials. It returns a non-fatal warning, if any.
func checkAuth(provider string) (warning string, err error) {
	keyed := map[string]string{"openai": "OPENAI_API_KEY", "openrouter": "OPENROUTER_API_KEY", "fireworks": "FIREWORKS_API_KEY"}
	switch provider {
	case "openai-codex":
		return checkCodexAuth()
	case "ollama":
		return "", nil
	default:
		env := keyed[provider]
		if strings.TrimSpace(os.Getenv("UNREAL_HARNESS_LLM_API_KEY")) == "" && strings.TrimSpace(os.Getenv(env)) == "" {
			return "", fmt.Errorf("provider %s needs %s or UNREAL_HARNESS_LLM_API_KEY", provider, env)
		}
		return "", nil
	}
}

func checkCodexAuth() (string, error) {
	if strings.TrimSpace(os.Getenv("OPENAI_CODEX_ACCESS_TOKEN")) != "" {
		return "", nil
	}
	path := strings.TrimSpace(os.Getenv("OPENAI_CODEX_AUTH_FILE"))
	if path == "" {
		home := strings.TrimSpace(os.Getenv("CODEX_HOME"))
		if home == "" {
			userHome, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			home = filepath.Join(userHome, ".codex")
		}
		path = filepath.Join(home, "auth.json")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("codex auth not found at %s: run `codex login`", path)
	}
	var auth struct {
		Tokens struct {
			AccessToken string `json:"access_token"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(data, &auth); err != nil || auth.Tokens.AccessToken == "" {
		return "", fmt.Errorf("%s has no access token: run `codex login`", path)
	}
	// The runner never refreshes tokens, so check expiry up front.
	exp, ok := jwtExpiry(auth.Tokens.AccessToken)
	if !ok {
		return "", nil
	}
	left := time.Until(exp)
	if left <= 0 {
		return "", fmt.Errorf("codex access token expired %s ago: run `codex login`", (-left).Round(time.Minute))
	}
	if left < time.Hour {
		return fmt.Sprintf("codex access token expires in %s; run `codex login` soon", left.Round(time.Minute)), nil
	}
	return "", nil
}

func jwtExpiry(token string) (time.Time, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return time.Time{}, false
	}
	var claims struct {
		Exp float64 `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.Exp == 0 {
		return time.Time{}, false
	}
	return time.Unix(int64(claims.Exp), 0), true
}

// isWithin reports whether path is dir or inside it.
func isWithin(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// realPath resolves symlinks where possible (macOS /tmp -> /private/tmp).
func realPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

// parseSize parses sizes like 500M, 5G, 1024 (bytes). "0" disables the limit.
func parseSize(s string) (int64, error) {
	s = strings.ToUpper(strings.TrimSuffix(strings.TrimSpace(s), "B"))
	mult := int64(1)
	if n := len(s); n > 0 {
		switch s[n-1] {
		case 'K':
			mult, s = 1<<10, s[:n-1]
		case 'M':
			mult, s = 1<<20, s[:n-1]
		case 'G':
			mult, s = 1<<30, s[:n-1]
		}
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	return int64(v * float64(mult)), nil
}

func defaultStateDir() string {
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "unreal-agent")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "unreal-agent")
}
