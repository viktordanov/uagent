package core_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/viktordanov/uagent/core"
)

func TestClassify(t *testing.T) {
	tests := map[string]struct {
		termination core.Termination
		exitCode    int
		reported    bool
		want        core.Status
	}{
		"clean exit":            {termination: core.TerminationExited, want: core.StatusOK},
		"nonzero exit":          {termination: core.TerminationExited, exitCode: 1, want: core.StatusFailed},
		"reported error":        {termination: core.TerminationExited, reported: true, want: core.StatusFailed},
		"timeout wins":          {termination: core.TerminationTimeout, exitCode: -1, reported: true, want: core.StatusTimeout},
		"interrupt":             {termination: core.TerminationInterrupted, exitCode: -1, want: core.StatusInterrupted},
		"disk limit":            {termination: core.TerminationDiskLimit, exitCode: -1, want: core.StatusDiskLimit},
		"killed but reported 0": {termination: core.TerminationTimeout, want: core.StatusTimeout},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, core.Classify(tc.termination, tc.exitCode, tc.reported))
		})
	}
}

func TestTriage(t *testing.T) {
	dotenv := core.Finding{Code: core.FindingDotenvRisky, Severity: core.SeverityBlocking, Message: "risky .env"}
	auth := core.Finding{Code: core.FindingAuthMissing, Severity: core.SeverityBlocking, Message: "run codex login"}
	expiring := core.Finding{Code: core.FindingAuthExpiring, Severity: core.SeverityWarning, Message: "expires soon"}

	t.Run("blocking findings block", func(t *testing.T) {
		blocking, warnings := core.Triage([]core.Finding{dotenv, auth, expiring}, false)

		assert.Equal(t, []core.Finding{dotenv, auth}, blocking)
		assert.Equal(t, []core.Finding{expiring}, warnings)
		assert.Equal(t, "risky .env; run codex login", core.Messages(blocking))
	})

	t.Run("allow-dotenv downgrades only the dotenv finding", func(t *testing.T) {
		blocking, warnings := core.Triage([]core.Finding{dotenv, auth}, true)

		assert.Equal(t, []core.Finding{auth}, blocking)
		assert.Equal(t, []core.Finding{dotenv}, warnings)
	})
}

func TestNewRunID(t *testing.T) {
	started := time.Date(2026, 9, 23, 17, 28, 31, 0, time.UTC)

	assert.Equal(t, "20260923-172831-2b1a9068", core.NewRunID(started, "2b1a9068-603e-48b9-bfcd-a9217088e04c"))
	assert.Equal(t, "20260923-172831-abc", core.NewRunID(started, "abc"))
	assert.Equal(t, "20260923-172831-4891e501", core.NewRunID(started, "subagent-4891e501-de28-460c-95e4-44c37d3d7522"))
}
