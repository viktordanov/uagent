// Package preflight checks the environment before a run: workspace, state
// directory placement, workspace .env contents, and provider credentials.
package preflight

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/viktordanov/uagent/domain"
)

const (
	FindingWorkspaceMissing = "workspace_missing"
	FindingStateInWorkspace = "state_in_workspace"
	FindingAuthMissing      = "auth_missing"
	FindingAuthExpired      = "auth_expired"
	FindingAuthExpiring     = "auth_expiring"
)

const expiryWarning = time.Hour

// Config configures the checks.
type Config struct {
	StateDir string
	// Getenv reads the environment (default os.Getenv).
	Getenv func(string) string
}

type checker struct {
	stateDir string
	getenv   func(string) string
}

func New(cfg Config) domain.Preflight {
	getenv := cfg.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}

	return &checker{stateDir: cfg.StateDir, getenv: getenv}
}

func (c *checker) Check(_ context.Context, req domain.RunRequest) ([]domain.Finding, error) {
	if info, err := os.Stat(req.Workspace); err != nil || !info.IsDir() {
		return []domain.Finding{blocking(FindingWorkspaceMissing, fmt.Sprintf("workspace %s is not a directory", req.Workspace))}, nil
	}
	var findings []domain.Finding
	// unreal-agent issue #3: sessions inside the workspace get read back by the
	// agent's own background greps and grow without bound.
	if isWithin(realPath(c.stateDir), realPath(req.Workspace)) {
		findings = append(findings, blocking(FindingStateInWorkspace,
			fmt.Sprintf("state dir %s is inside the workspace; pick one outside it with --state-dir", c.stateDir)))
	}
	risky, err := riskyDotenvKeys(req.Workspace)
	if err != nil {
		return nil, err
	}
	if len(risky) > 0 {
		findings = append(findings, blocking(domain.FindingDotenvRisky, fmt.Sprintf(
			"workspace .env sets %s, which can redirect the model endpoint or credentials (override with --allow-dotenv)",
			strings.Join(risky, ", "))))
	}
	auth, err := c.checkAuth(req.Provider)
	if err != nil {
		return nil, err
	}

	return append(findings, auth...), nil
}

func blocking(code, msg string) domain.Finding {
	return domain.Finding{Code: code, Severity: domain.SeverityBlocking, Message: msg}
}

// riskyDotenvKeys guards against unreal-agent issue #5: the runner loads
// <workspace>/.env and lets it redirect the endpoint or credentials.
func riskyDotenvKeys(workspace string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(workspace, ".env"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read workspace .env: %w", err)
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
		strings.HasSuffix(upper, "_PROXY")
}

var apiKeyEnv = map[string]string{
	"openai":     "OPENAI_API_KEY",
	"openrouter": "OPENROUTER_API_KEY",
	"fireworks":  "FIREWORKS_API_KEY",
}

func (c *checker) checkAuth(provider string) ([]domain.Finding, error) {
	switch provider {
	case "openai-codex":
		return c.checkCodexAuth()
	case "ollama":
		return nil, nil
	}
	env := apiKeyEnv[provider]
	if strings.TrimSpace(c.getenv("UNREAL_HARNESS_LLM_API_KEY")) == "" && strings.TrimSpace(c.getenv(env)) == "" {
		return []domain.Finding{blocking(FindingAuthMissing, fmt.Sprintf("provider %s needs %s or UNREAL_HARNESS_LLM_API_KEY", provider, env))}, nil
	}

	return nil, nil
}

func (c *checker) checkCodexAuth() ([]domain.Finding, error) {
	if strings.TrimSpace(c.getenv("OPENAI_CODEX_ACCESS_TOKEN")) != "" {
		return nil, nil
	}
	path, err := c.codexAuthPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return []domain.Finding{blocking(FindingAuthMissing, fmt.Sprintf("codex auth not found at %s: run `codex login`", path))}, nil
	}
	var auth struct {
		Tokens struct {
			AccessToken string `json:"access_token"`
		} `json:"tokens"`
	}
	if json.Unmarshal(data, &auth) != nil || auth.Tokens.AccessToken == "" {
		return []domain.Finding{blocking(FindingAuthMissing, path+" has no access token: run `codex login`")}, nil
	}
	// The runner never refreshes tokens, so check expiry up front.
	exp, ok := jwtExpiry(auth.Tokens.AccessToken)
	if !ok {
		return nil, nil
	}
	switch left := time.Until(exp); {
	case left <= 0:
		return []domain.Finding{blocking(FindingAuthExpired,
			fmt.Sprintf("codex access token expired %s ago: run `codex login`", (-left).Round(time.Minute)))}, nil
	case left < expiryWarning:
		return []domain.Finding{{
			Code: FindingAuthExpiring, Severity: domain.SeverityWarning,
			Message: fmt.Sprintf("codex access token expires in %s; run `codex login` soon", left.Round(time.Minute)),
		}}, nil
	}

	return nil, nil
}

func (c *checker) codexAuthPath() (string, error) {
	if p := strings.TrimSpace(c.getenv("OPENAI_CODEX_AUTH_FILE")); p != "" {
		return p, nil
	}
	home := strings.TrimSpace(c.getenv("CODEX_HOME"))
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to find home directory: %w", err)
		}
		home = filepath.Join(userHome, ".codex")
	}

	return filepath.Join(home, "auth.json"), nil
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

func isWithin(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)

	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// realPath resolves symlinks (macOS /tmp -> /private/tmp). For a path that does
// not exist yet it resolves the nearest existing ancestor and keeps the rest.
func realPath(path string) string {
	path = filepath.Clean(path)
	var rest []string
	for {
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			return filepath.Join(append([]string{resolved}, rest...)...)
		}
		parent := filepath.Dir(path)
		if parent == path {
			return filepath.Join(append([]string{path}, rest...)...)
		}
		rest = append([]string{filepath.Base(path)}, rest...)
		path = parent
	}
}
