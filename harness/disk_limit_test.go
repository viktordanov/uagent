package harness_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/viktordanov/uagent/core"
	"github.com/viktordanov/uagent/harness"
	"github.com/viktordanov/uagent/testing/fixtures"
)

// diskBackend starts an agent that writes files of the given sizes under
// the session's operations directory, then works until it is stopped or
// for runFor, whichever comes first.
type diskBackend struct {
	files  map[string]int
	runFor time.Duration
}

func (b diskBackend) Start(_ context.Context, l harness.Launch) (harness.Process, error) {
	ops := filepath.Join(l.SessionsDir, "operations", l.Request.SessionID)
	for name, size := range b.files {
		path := filepath.Join(ops, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
			return nil, err
		}
	}
	p := &memProcess{done: make(chan struct{}), stop: make(chan struct{})}
	go func() {
		defer close(p.done)
		_, _ = l.Stdout.Write(fixtures.RunnerOutput("simple.jsonl"))
		_ = l.Stdout.Close()
		select {
		case <-p.stop:
			p.code = 130
		case <-time.After(b.runFor):
		}
	}()

	return p, nil
}

func TestRun_DiskLimit(t *testing.T) {
	const limit = 10 << 10
	tests := map[string]struct {
		files map[string]int
		want  core.Status
	}{
		"tool output past the limit stops the run": {
			files: map[string]int{"op-1/out": limit, "op-2/out": limit},
			want:  core.StatusDiskLimit,
		},
		"an excluded scratch directory does not count": {
			files: map[string]int{"op-1/out": limit / 2, "tmp/go-build/ab/cache-d": 100 * limit, "review-tmp/x": 100 * limit},
			want:  core.StatusOK,
		},
		"an excluded name counts below the top level": {
			files: map[string]int{"op-1/tmp/out": 2 * limit},
			want:  core.StatusDiskLimit,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			harness.SetDiskPollInterval(t, time.Millisecond)
			e := newTestEnv(t, "simple.jsonl")
			h := harness.New(harness.Config{
				Backend:  diskBackend{files: tc.files, runFor: 200 * time.Millisecond},
				StateDir: e.stateDir, KillGrace: 300 * time.Millisecond,
				MaxDisk: limit, DiskExclude: []string{"tmp", "review-tmp"},
				Getenv: func(k string) string {
					if k == "CODEX_HOME" {
						return e.codexHome
					}

					return ""
				},
			})

			result, err := h.Run(context.Background(), e.request(), func(core.Event) {})

			require.NoError(t, err)
			assert.Equal(t, tc.want, result.Status)
		})
	}
}
