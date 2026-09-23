package harness_test

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/viktordanov/uagent/core"
	"github.com/viktordanov/uagent/harness"
	"github.com/viktordanov/uagent/testing/fixtures"
)

type preflightEnv struct {
	workspace string
	codexHome string
	env       map[string]string
	h         *harness.Harness
}

func newPreflightEnv(t *testing.T) *preflightEnv {
	t.Helper()
	root := t.TempDir()
	h := &preflightEnv{
		workspace: filepath.Join(root, "workspace"),
		codexHome: filepath.Join(root, "codex"),
		env:       map[string]string{},
	}
	require.NoError(t, os.MkdirAll(h.workspace, 0o700))
	require.NoError(t, os.MkdirAll(h.codexHome, 0o700))
	h.env["CODEX_HOME"] = h.codexHome
	h.h = harness.New(harness.Config{
		StateDir: filepath.Join(root, "state"),
		Getenv:   func(k string) string { return h.env[k] },
	})
	h.writeToken(t, 24*time.Hour)

	return h
}

func (h *preflightEnv) writeToken(t *testing.T, expiresIn time.Duration) {
	t.Helper()
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"exp":` + strconv.FormatInt(time.Now().Add(expiresIn).Unix(), 10) + `}`))
	auth := `{"tokens":{"access_token":"x.` + payload + `.y"}}`
	require.NoError(t, os.WriteFile(filepath.Join(h.codexHome, "auth.json"), []byte(auth), 0o600))
}

func (h *preflightEnv) check(t *testing.T, provider string) []core.Finding {
	t.Helper()
	req := fixtures.RequestWith(func(r *core.Request) {
		r.Workspace = h.workspace
		r.Provider = provider
	})
	findings, err := h.h.Preflight(req)
	require.NoError(t, err)

	return findings
}

func codes(findings []core.Finding) []string {
	out := make([]string, 0, len(findings))
	for _, f := range findings {
		out = append(out, f.Code)
	}

	return out
}

func TestPreflight(t *testing.T) {
	t.Run("clean workspace with valid codex auth has no findings", func(t *testing.T) {
		h := newPreflightEnv(t)

		assert.Empty(t, h.check(t, "openai-codex"))
	})

	t.Run("risky .env keys block", func(t *testing.T) {
		h := newPreflightEnv(t)
		dotenv := "FOO=1\nexport UNREAL_HARNESS_LLM_BASE_URL=http://evil\nHTTPS_PROXY=x\n# CODEX_HOME=ignored\n"
		require.NoError(t, os.WriteFile(filepath.Join(h.workspace, ".env"), []byte(dotenv), 0o600))

		findings := h.check(t, "openai-codex")

		require.Len(t, findings, 1)
		assert.Equal(t, core.FindingDotenvRisky, findings[0].Code)
		assert.Equal(t, core.SeverityBlocking, findings[0].Severity)
		assert.Contains(t, findings[0].Message, "UNREAL_HARNESS_LLM_BASE_URL, HTTPS_PROXY")
	})

	t.Run("state dir inside the workspace blocks", func(t *testing.T) {
		h := newPreflightEnv(t)
		h.h = harness.New(harness.Config{
			StateDir: filepath.Join(h.workspace, ".state"),
			Getenv:   func(k string) string { return h.env[k] },
		})

		assert.Equal(t, []string{core.FindingStateInWorkspace}, codes(h.check(t, "openai-codex")))
	})

	t.Run("missing workspace blocks", func(t *testing.T) {
		h := newPreflightEnv(t)
		require.NoError(t, os.RemoveAll(h.workspace))

		assert.Equal(t, []string{core.FindingWorkspaceMissing}, codes(h.check(t, "openai-codex")))
	})

	t.Run("codex token expiry", func(t *testing.T) {
		tests := map[string]struct {
			expiresIn time.Duration
			want      []string
			severity  core.Severity
		}{
			"expired":       {expiresIn: -time.Hour, want: []string{core.FindingAuthExpired}, severity: core.SeverityBlocking},
			"expiring soon": {expiresIn: 30 * time.Minute, want: []string{core.FindingAuthExpiring}, severity: core.SeverityWarning},
		}
		for name, tc := range tests {
			t.Run(name, func(t *testing.T) {
				h := newPreflightEnv(t)
				h.writeToken(t, tc.expiresIn)

				findings := h.check(t, "openai-codex")

				assert.Equal(t, tc.want, codes(findings))
				assert.Equal(t, tc.severity, findings[0].Severity)
			})
		}
	})

	t.Run("missing credentials block", func(t *testing.T) {
		h := newPreflightEnv(t)
		require.NoError(t, os.Remove(filepath.Join(h.codexHome, "auth.json")))

		assert.Equal(t, []string{core.FindingAuthMissing}, codes(h.check(t, "openai-codex")))
		assert.Equal(t, []string{core.FindingAuthMissing}, codes(h.check(t, "openrouter")))
		assert.Empty(t, h.check(t, "ollama"))
	})

	t.Run("a provider without a default model needs --model", func(t *testing.T) {
		h := newPreflightEnv(t)
		h.env["OPENROUTER_API_KEY"] = "sk-or"
		h.env["OPENAI_API_KEY"] = "sk-test"
		noModel := func(provider string) []string {
			req := fixtures.RequestWith(func(r *core.Request) {
				r.Workspace, r.Provider, r.Model = h.workspace, provider, ""
			})
			findings, err := h.h.Preflight(req)
			require.NoError(t, err)

			return codes(findings)
		}

		assert.Equal(t, []string{core.FindingModelMissing}, noModel("openrouter"))
		assert.Equal(t, []string{core.FindingModelMissing}, noModel("ollama"))
		assert.Empty(t, noModel("openai"), "the runner defaults openai to gpt-6-astra")
	})

	t.Run("api key from the environment satisfies keyed providers", func(t *testing.T) {
		h := newPreflightEnv(t)
		h.env["OPENAI_API_KEY"] = "sk-test"

		assert.Empty(t, h.check(t, "openai"))
	})
}
