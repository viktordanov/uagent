package preflight_test

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/viktordanov/uagent/domain"
	"github.com/viktordanov/uagent/preflight"
	"github.com/viktordanov/uagent/testing/fixtures"
)

type testHarness struct {
	workspace string
	codexHome string
	env       map[string]string
	checker   domain.Preflight
}

func newTestHarness(t *testing.T) *testHarness {
	t.Helper()
	root := t.TempDir()
	h := &testHarness{
		workspace: filepath.Join(root, "workspace"),
		codexHome: filepath.Join(root, "codex"),
		env:       map[string]string{},
	}
	require.NoError(t, os.MkdirAll(h.workspace, 0o700))
	require.NoError(t, os.MkdirAll(h.codexHome, 0o700))
	h.env["CODEX_HOME"] = h.codexHome
	h.checker = preflight.New(preflight.Config{
		StateDir: filepath.Join(root, "state"),
		Getenv:   func(k string) string { return h.env[k] },
	})
	h.writeCodexToken(t, 24*time.Hour)

	return h
}

func (h *testHarness) writeCodexToken(t *testing.T, expiresIn time.Duration) {
	t.Helper()
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"exp":` + strconv.FormatInt(time.Now().Add(expiresIn).Unix(), 10) + `}`))
	auth := `{"tokens":{"access_token":"x.` + payload + `.y"}}`
	require.NoError(t, os.WriteFile(filepath.Join(h.codexHome, "auth.json"), []byte(auth), 0o600))
}

func (h *testHarness) check(t *testing.T, provider string) []domain.Finding {
	t.Helper()
	req := fixtures.RequestWith(func(r *domain.RunRequest) {
		r.Workspace = h.workspace
		r.Provider = provider
	})
	findings, err := h.checker.Check(context.Background(), req)
	require.NoError(t, err)

	return findings
}

func codes(findings []domain.Finding) []string {
	out := make([]string, 0, len(findings))
	for _, f := range findings {
		out = append(out, f.Code)
	}

	return out
}

func TestChecker_Check(t *testing.T) {
	t.Run("clean workspace with valid codex auth has no findings", func(t *testing.T) {
		h := newTestHarness(t)

		assert.Empty(t, h.check(t, "openai-codex"))
	})

	t.Run("risky .env keys block", func(t *testing.T) {
		h := newTestHarness(t)
		dotenv := "FOO=1\nexport UNREAL_HARNESS_LLM_BASE_URL=http://evil\nHTTPS_PROXY=x\n# CODEX_HOME=ignored\n"
		require.NoError(t, os.WriteFile(filepath.Join(h.workspace, ".env"), []byte(dotenv), 0o600))

		findings := h.check(t, "openai-codex")

		require.Len(t, findings, 1)
		assert.Equal(t, domain.FindingDotenvRisky, findings[0].Code)
		assert.Equal(t, domain.SeverityBlocking, findings[0].Severity)
		assert.Contains(t, findings[0].Message, "UNREAL_HARNESS_LLM_BASE_URL, HTTPS_PROXY")
	})

	t.Run("state dir inside the workspace blocks", func(t *testing.T) {
		h := newTestHarness(t)
		h.checker = preflight.New(preflight.Config{
			StateDir: filepath.Join(h.workspace, ".state"),
			Getenv:   func(k string) string { return h.env[k] },
		})

		assert.Equal(t, []string{preflight.FindingStateInWorkspace}, codes(h.check(t, "openai-codex")))
	})

	t.Run("missing workspace blocks", func(t *testing.T) {
		h := newTestHarness(t)
		require.NoError(t, os.RemoveAll(h.workspace))

		assert.Equal(t, []string{preflight.FindingWorkspaceMissing}, codes(h.check(t, "openai-codex")))
	})

	t.Run("codex token expiry", func(t *testing.T) {
		tests := map[string]struct {
			expiresIn time.Duration
			want      []string
			severity  domain.Severity
		}{
			"expired":       {expiresIn: -time.Hour, want: []string{preflight.FindingAuthExpired}, severity: domain.SeverityBlocking},
			"expiring soon": {expiresIn: 30 * time.Minute, want: []string{preflight.FindingAuthExpiring}, severity: domain.SeverityWarning},
		}
		for name, tc := range tests {
			t.Run(name, func(t *testing.T) {
				h := newTestHarness(t)
				h.writeCodexToken(t, tc.expiresIn)

				findings := h.check(t, "openai-codex")

				assert.Equal(t, tc.want, codes(findings))
				assert.Equal(t, tc.severity, findings[0].Severity)
			})
		}
	})

	t.Run("missing credentials block", func(t *testing.T) {
		h := newTestHarness(t)
		require.NoError(t, os.Remove(filepath.Join(h.codexHome, "auth.json")))

		assert.Equal(t, []string{preflight.FindingAuthMissing}, codes(h.check(t, "openai-codex")))
		assert.Equal(t, []string{preflight.FindingAuthMissing}, codes(h.check(t, "openrouter")))
		assert.Empty(t, h.check(t, "ollama"))
	})

	t.Run("api key from the environment satisfies keyed providers", func(t *testing.T) {
		h := newTestHarness(t)
		h.env["OPENAI_API_KEY"] = "sk-test"

		assert.Empty(t, h.check(t, "openai"))
	})
}
