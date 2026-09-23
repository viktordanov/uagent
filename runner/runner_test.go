package runner_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/viktordanov/uagent/domain"
	"github.com/viktordanov/uagent/runner"
	"github.com/viktordanov/uagent/statedir"
	"github.com/viktordanov/uagent/testing/fixtures"
)

type fakeRunner struct {
	layout statedir.Layout
	runner domain.Runner
	dir    string
}

// newFakeRunner writes a shell script that stands in for unreal-agent-runner.
func newFakeRunner(t *testing.T, script string) *fakeRunner {
	t.Helper()
	if testing.Short() {
		t.Skip("subprocess test")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-runner")
	require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\n"+script+"\n"), 0o700))
	layout := statedir.Layout{Root: filepath.Join(dir, "state")}

	return &fakeRunner{
		layout: layout,
		runner: runner.New(runner.Config{Bin: bin, Layout: layout, KillGrace: 500 * time.Millisecond}),
		dir:    dir,
	}
}

func (f *fakeRunner) run(t *testing.T, ctx context.Context) (domain.RunnerExit, []domain.Event) {
	t.Helper()
	var events []domain.Event
	req := fixtures.RequestWith(func(r *domain.RunRequest) { r.Workspace = f.dir })
	exit, err := f.runner.Run(ctx, req, func(e domain.Event) { events = append(events, e) })
	require.NoError(t, err)

	return exit, events
}

func TestRunner_Run(t *testing.T) {
	t.Run("streams events and records the run", func(t *testing.T) {
		fixture := filepath.Join(t.TempDir(), "out.jsonl")
		require.NoError(t, os.WriteFile(fixture, fixtures.RunnerOutput("simple.jsonl"), 0o600))
		f := newFakeRunner(t, `env > "$(dirname "$0")/env.txt"; cat > "$(dirname "$0")/stdin.json"; cat "`+fixture+`"`)

		exit, events := f.run(t, context.Background())

		assert.Equal(t, domain.RunnerExit{Code: 0, Termination: domain.TerminationExited, EndedAt: exit.EndedAt}, exit)
		assert.NotEmpty(t, events)
		runDir := f.layout.RunDir(fixtures.RunID)
		saved, err := os.ReadFile(filepath.Join(runDir, statedir.EventsFile))
		require.NoError(t, err)
		assert.Equal(t, fixtures.RunnerOutput("simple.jsonl"), saved)
		stdin, err := os.ReadFile(filepath.Join(f.dir, "stdin.json"))
		require.NoError(t, err)
		assert.JSONEq(t, `{"prompt":"Summarize this project.","thinking_level":"high","model":"gpt-6-sol","session_id":"`+fixtures.SessionID+`"}`, string(stdin))
		env, err := os.ReadFile(filepath.Join(f.dir, "env.txt"))
		require.NoError(t, err)
		assert.Contains(t, string(env), "UNREAL_HARNESS_LLM_PROVIDER=openai-codex\n")
		assert.Contains(t, string(env), "UNREAL_HARNESS_LLM_BASE_URL=\n", "base URL is pinned so a workspace .env cannot set it")
	})

	t.Run("reports a nonzero exit code", func(t *testing.T) {
		f := newFakeRunner(t, `echo '{"type":"error","message":"model must be set"}'; exit 3`)

		exit, events := f.run(t, context.Background())

		assert.Equal(t, 3, exit.Code)
		assert.Equal(t, domain.TerminationExited, exit.Termination)
		require.Len(t, events, 1)
		assert.IsType(t, domain.RunnerError{}, events[0])
	})

	t.Run("cancellation kills the whole process group", func(t *testing.T) {
		f := newFakeRunner(t, `sleep 300 & echo $! > "$(dirname "$0")/child.pid"; wait`)
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()

		exit, _ := f.run(t, ctx)

		assert.Equal(t, domain.TerminationCanceled, exit.Termination)
		pidText, err := os.ReadFile(filepath.Join(f.dir, "child.pid"))
		require.NoError(t, err)
		pid, err := strconv.Atoi(strings.TrimSpace(string(pidText)))
		require.NoError(t, err)
		assert.Eventually(t, func() bool {
			return syscall.Kill(pid, 0) != nil
		}, 2*time.Second, 20*time.Millisecond, "background child %d should be dead", pid)
	})
}
