package harness_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/viktordanov/uagent/core"
	"github.com/viktordanov/uagent/harness"
	"github.com/viktordanov/uagent/testing/fixtures"
)

// memBackend writes a fixture's runner output in process, then either exits
// or waits for an interrupt.
type memBackend struct {
	output []byte
	hang   bool
	got    harness.Launch
}

func (b *memBackend) Start(_ context.Context, l harness.Launch) (harness.Process, error) {
	b.got = l
	p := &memProcess{done: make(chan struct{}), stop: make(chan struct{})}
	go func() {
		defer close(p.done)
		_, _ = l.Stdout.Write(b.output)
		_ = l.Stdout.Close()
		if b.hang {
			<-p.stop
			p.code = 130
		}
	}()

	return p, nil
}

type memProcess struct {
	done, stop chan struct{}
	once       sync.Once
	code       int
}

func (p *memProcess) Done() <-chan struct{} { return p.done }
func (p *memProcess) ExitCode() int         { return p.code }
func (p *memProcess) Interrupt()            { p.once.Do(func() { close(p.stop) }) }
func (p *memProcess) Terminate()            { p.Interrupt() }
func (p *memProcess) Kill()                 { p.Interrupt() }

func newBackendEnv(t *testing.T, b harness.Backend) (*harness.Harness, core.Request) {
	t.Helper()
	e := newTestEnv(t, "simple.jsonl")
	h := harness.New(harness.Config{
		Backend: b, StateDir: e.stateDir, KillGrace: 300 * time.Millisecond,
		Getenv: func(k string) string {
			if k == "CODEX_HOME" {
				return e.codexHome
			}

			return ""
		},
	})

	return h, e.request()
}

func TestBackend_InProcess(t *testing.T) {
	b := &memBackend{output: fixtures.RunnerOutput("simple.jsonl")}
	h, req := newBackendEnv(t, b)

	var events []core.Event
	result, err := h.Run(context.Background(), req, func(e core.Event) { events = append(events, e) })

	require.NoError(t, err)
	assert.Equal(t, core.StatusOK, result.Status)
	assert.Equal(t, "hello", result.Answer)
	assert.Equal(t, 2, result.Stats.ToolCalls)
	assert.Equal(t, "sessions", filepath.Base(b.got.SessionsDir))
	assert.Contains(t, string(b.got.RunnerRequest), `"prompt":"Summarize this project."`)
	saved, err := os.ReadFile(filepath.Join(h.RunDir(result.Request.RunID), harness.EventsFile))
	require.NoError(t, err)
	assert.Equal(t, fixtures.RunnerOutput("simple.jsonl"), saved, "events.jsonl is the backend's output byte for byte")
	assert.IsType(t, core.RunFinished{}, events[len(events)-1])
}

func TestBackend_Interrupt(t *testing.T) {
	b := &memBackend{output: fixtures.RunnerOutput("simple.jsonl"), hang: true}
	h, req := newBackendEnv(t, b)

	run, err := h.Start(context.Background(), req, func(core.Event) {})
	require.NoError(t, err)
	assert.IsType(t, &memProcess{}, run.Process(), "the engine can reach its own process type")
	run.Interrupt()
	result, err := run.Wait()

	require.NoError(t, err)
	assert.Equal(t, core.StatusInterrupted, result.Status)
}

// stuckBackend starts an agent that writes its output and then ignores
// every signal: Done never closes.
type stuckBackend struct{ output []byte }

func (b stuckBackend) Start(_ context.Context, l harness.Launch) (harness.Process, error) {
	_, _ = l.Stdout.Write(b.output)
	_ = l.Stdout.Close()

	return stuckProcess{}, nil
}

type stuckProcess struct{}

func (stuckProcess) Done() <-chan struct{} { return nil }
func (stuckProcess) ExitCode() int         { panic("ExitCode before Done") }
func (stuckProcess) Interrupt()            {}
func (stuckProcess) Terminate()            {}
func (stuckProcess) Kill()                 {}

// TestBackend_KillDuringInterruptIsBounded: a kill cuts a graceful stop
// short, and an agent that outlives the kill is abandoned after the grace
// period instead of holding the run open.
func TestBackend_KillDuringInterruptIsBounded(t *testing.T) {
	const grace = 300 * time.Millisecond // newBackendEnv's KillGrace
	h, req := newBackendEnv(t, stuckBackend{output: fixtures.RunnerOutput("simple.jsonl")})

	run, err := h.Start(context.Background(), req, func(core.Event) {})
	require.NoError(t, err)
	run.Interrupt()
	time.Sleep(grace / 6)
	killed := time.Now()
	run.Kill()
	select {
	case <-run.Done():
	case <-time.After(10 * grace):
		t.Fatal("the run never ended")
	}

	// SIGTERM's grace, then SIGKILL's; without the kill the interrupt's grace comes first.
	assert.Less(t, time.Since(killed), 2*grace+grace/2)
	result, err := run.Wait()
	require.NoError(t, err)
	assert.Equal(t, core.StatusInterrupted, result.Status)
	assert.Equal(t, -1, result.RunnerExitCode, "an abandoned agent has no exit code")
}

func TestRunnerBackend_Env(t *testing.T) {
	e := newTestEnv(t, "simple.jsonl")
	t.Setenv("FAKERUNNER_CAPTURE", e.capture)
	h := harness.New(harness.Config{
		Backend:  harness.RunnerBackend{Path: fakeRunner, Env: []string{"SHELL=/custom/shell"}},
		StateDir: e.stateDir, Getenv: func(k string) string {
			if k == "CODEX_HOME" {
				return e.codexHome
			}

			return ""
		},
	})
	_, err := h.Run(context.Background(), e.request(), func(core.Event) {})
	require.NoError(t, err)
	env, err := os.ReadFile(filepath.Join(e.capture, "env.txt"))
	require.NoError(t, err)
	assert.Contains(t, string(env), "SHELL=/custom/shell")
}

// TestStart_KillsToolsLeftByACrash: a session file that still records a live
// tool (its run was killed before cleaning up) gets that tool's process
// group killed when the next run of the session starts.
func TestStart_KillsToolsLeftByACrash(t *testing.T) {
	e := newTestEnv(t, "simple.jsonl")
	sleeper := exec.Command("/bin/sleep", "30")
	sleeper.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	require.NoError(t, sleeper.Start())
	done := make(chan error, 1)
	go func() { done <- sleeper.Wait() }()
	t.Cleanup(func() { _ = syscall.Kill(-sleeper.Process.Pid, syscall.SIGKILL) })

	id := "3f2a1b2c-0000-4000-8000-00000000c0de"
	sessions := filepath.Join(e.stateDir, "sessions")
	require.NoError(t, os.MkdirAll(sessions, 0o700))
	record := fmt.Sprintf(`{"type":"operation","data":{"Operation":{"ID":"op-1","Status":"running","State":{"ProcessGroupID":%d}}}}`+"\n", sleeper.Process.Pid)
	require.NoError(t, os.WriteFile(filepath.Join(sessions, id+".session.jsonl"), []byte(record), 0o600))

	// The new run hangs, so only Start (not the end of a run) can kill it.
	t.Setenv("FAKERUNNER_HANG", "1")
	run, err := e.h.Start(context.Background(), e.request(func(r *core.Request) { r.SessionID = id }), func(core.Event) {})
	require.NoError(t, err)
	defer func() { run.Kill(); _, _ = run.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the orphaned tool is still running while the new run is live")
	}
}
