package main_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/viktordanov/uagent/testing/fixtures"
)

var bin struct{ uagent, fakeRunner string }

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "uagent-cli")
	if err != nil {
		panic(err)
	}
	bin.uagent, bin.fakeRunner = filepath.Join(dir, "uagent"), filepath.Join(dir, "fakerunner")
	for out, pkg := range map[string]string{bin.uagent: ".", bin.fakeRunner: "github.com/viktordanov/uagent/testing/fakerunner"} {
		if msg, err := exec.Command("go", "build", "-o", out, pkg).CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "build %s: %v\n%s", pkg, err, msg)
			os.Exit(1)
		}
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

type cliResult struct {
	code           int
	stdout, stderr string
}

// uagent runs the built CLI against the fake runner replaying fixture.
func uagent(t *testing.T, fixture string, env []string, args ...string) cliResult {
	t.Helper()

	return uagentStdin(t, fixture, env, "", args...)
}

// uagentStdin is uagent with a prompt piped on stdin.
func uagentStdin(t *testing.T, fixture string, env []string, stdin string, args ...string) cliResult {
	t.Helper()
	root := t.TempDir()
	workspace, codexHome := filepath.Join(root, "workspace"), filepath.Join(root, "codex")
	require.NoError(t, os.MkdirAll(workspace, 0o700))
	require.NoError(t, os.MkdirAll(codexHome, 0o700))
	claims := `{"exp":` + strconv.FormatInt(time.Now().Add(time.Hour*24).Unix(), 10) + `}`
	auth := `{"tokens":{"access_token":"x.` + base64.RawURLEncoding.EncodeToString([]byte(claims)) + `.y"}}`
	require.NoError(t, os.WriteFile(filepath.Join(codexHome, "auth.json"), []byte(auth), 0o600))
	if strings.Contains(strings.Join(env, " "), "DOTENV=") {
		require.NoError(t, os.WriteFile(filepath.Join(workspace, ".env"), []byte("UNREAL_HARNESS_LLM_BASE_URL=http://evil\n"), 0o600))
	}

	cmd := exec.Command(bin.uagent, append([]string{"-C", workspace, "--state-dir", filepath.Join(root, "state")}, args...)...)
	cmd.Env = append(os.Environ(),
		"UAGENT_RUNNER="+bin.fakeRunner,
		"CODEX_HOME="+codexHome,
		"FAKERUNNER_FIXTURE="+fixtures.Path(fixture),
		"NO_COLOR=1",
	)
	cmd.Env = append(cmd.Env, env...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code = exitErr.ExitCode()
	} else {
		require.NoError(t, err)
	}

	return cliResult{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

func TestCLI(t *testing.T) {
	t.Run("prints the answer on stdout and the summary on stderr", func(t *testing.T) {
		r := uagent(t, "simple.jsonl", nil, "Summarize this project.")

		assert.Equal(t, 0, r.code, r.stderr)
		assert.Equal(t, "hello\n", r.stdout)
		assert.Contains(t, r.stderr, "→ Bash  ls")
		assert.Contains(t, r.stderr, "status   ok")
		assert.Contains(t, r.stderr, "tool calls 2 (Bash 2) · 0 failed · max 2 parallel")
	})

	t.Run("--json prints the summary with the answer", func(t *testing.T) {
		r := uagent(t, "parallel.jsonl", nil, "--json", "-q", "prompt")

		require.Equal(t, 0, r.code, r.stderr)
		var summary struct {
			Status string `json:"status"`
			Answer string `json:"answer"`
			Stats  struct {
				Turns int `json:"turns"`
			} `json:"stats"`
		}
		require.NoError(t, json.Unmarshal([]byte(r.stdout), &summary))
		assert.Equal(t, "ok", summary.Status)
		assert.Equal(t, "A; B", summary.Answer)
		assert.Equal(t, 4, summary.Stats.Turns)
	})

	t.Run("--stream writes only JSONL events to stdout", func(t *testing.T) {
		r := uagent(t, "simple.jsonl", nil, "--stream", "prompt")

		require.Equal(t, 0, r.code, r.stderr)
		lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
		assert.Contains(t, lines[0], `"type":"run_started"`)
		assert.Contains(t, lines[len(lines)-1], `"type":"run_finished"`)
		assert.Empty(t, r.stderr)
	})

	t.Run("reads the prompt from stdin", func(t *testing.T) {
		r := uagentStdin(t, "simple.jsonl", []string{"FAKERUNNER_CAPTURE=" + t.TempDir()}, "Prompt from a pipe.", "-q")

		assert.Equal(t, 0, r.code, r.stderr)
		assert.Equal(t, "hello\n", r.stdout)
	})

	t.Run("without a prompt it asks for one", func(t *testing.T) {
		r := uagent(t, "simple.jsonl", nil, "-q")

		assert.Equal(t, 2, r.code)
		assert.Contains(t, r.stderr, "no prompt")
	})

	t.Run("exit codes", func(t *testing.T) {
		tests := map[string]struct {
			fixture string
			env     []string
			args    []string
			want    int
		}{
			"runner failure":    {fixture: "error.jsonl", env: []string{"FAKERUNNER_EXIT=1"}, want: 1},
			"preflight blocked": {fixture: "simple.jsonl", env: []string{"DOTENV=1"}, want: 2},
			"bad flag":          {fixture: "simple.jsonl", args: []string{"--effort", "huge"}, want: 2},
			"timeout":           {fixture: "timeout.jsonl", env: []string{"FAKERUNNER_HANG=1"}, args: []string{"-t", "300ms"}, want: 124},
		}
		for name, tc := range tests {
			t.Run(name, func(t *testing.T) {
				r := uagent(t, tc.fixture, tc.env, append(tc.args, "-q", "prompt")...)

				assert.Equal(t, tc.want, r.code, r.stderr)
			})
		}
	})
}
