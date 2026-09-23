package harness_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
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
