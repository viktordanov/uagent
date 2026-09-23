package harness

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/viktordanov/uagent/core"
)

// Run is a started run. Its methods are safe to call from any goroutine.
type Run struct {
	h         *Harness
	req       core.Request
	started   time.Time
	sink      core.Sink
	collector *core.StatsCollector
	proc      *process

	ctx    context.Context
	cancel context.CancelFunc
	unlock func() error

	interrupt, kill         chan struct{}
	interruptOnce, killOnce sync.Once

	done   chan struct{}
	result core.Result
	err    error
}

// SessionID is the runner session this run belongs to.
func (r *Run) SessionID() string { return r.req.SessionID }

// RunID names the run's directory under the state directory.
func (r *Run) RunID() string { return r.req.RunID }

// Interrupt asks the runner to stop gracefully: SIGINT first, so it can record
// its state, then a kill after the grace period. The run ends as interrupted.
func (r *Run) Interrupt() {
	r.interruptOnce.Do(func() { close(r.interrupt) })
}

// Kill stops the runner and its tools at once: SIGTERM, then SIGKILL after the
// grace period. The run ends as interrupted.
func (r *Run) Kill() {
	r.killOnce.Do(func() { close(r.kill) })
}

// Done is closed when the run has finished and Wait would not block.
func (r *Run) Done() <-chan struct{} { return r.done }

// Wait blocks until the run ends. It returns the Result for every run, and an
// error only when the summary could not be saved.
func (r *Run) Wait() (core.Result, error) {
	<-r.done

	return r.result, r.err
}

func (r *Run) emit(e core.Event) {
	r.collector.Add(e)
	r.sink(e)
}

// supervise waits for the process to end, records the result, and releases the lock.
func (r *Run) supervise() {
	defer close(r.done)
	defer r.cancel()
	termination := r.proc.supervise(r.ctx, r.interrupt, r.kill)
	exit := r.proc.finish(r.ctx, termination)
	r.collector.Close(exit.endedAt)

	r.result = core.Result{
		Request:        r.req,
		Status:         core.Classify(exit.termination, exit.code, r.collector.HasError()),
		RunnerExitCode: exit.code,
		StartedAt:      r.started,
		Wall:           exit.endedAt.Sub(r.started),
		Stats:          r.collector.Stats(),
		Answer:         r.collector.Answer(),
	}
	saveErr := r.h.layout.saveSummary(r.result)
	r.sink(core.RunFinished{At: exit.endedAt, Result: r.result})
	r.err = errors.Join(saveErr, r.unlock())
}
