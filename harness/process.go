package harness

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/viktordanov/uagent/core"
)

const (
	drainTimeout     = 2 * time.Second
	diskPollInterval = 5 * time.Second
)

// processExit describes how the runner process ended.
type processExit struct {
	code        int
	termination core.Termination
	endedAt     time.Time
}

// process is one running agent, started by the backend.
type process struct {
	h           *Harness
	agent       Process
	log         *slog.Logger
	sessionFile string
	opsDir      string
	pipe        *os.File
	events      *os.File
	stderr      *os.File
	readDone    chan struct{}
}

// startProcess writes the run records (request.json, a running summary.json,
// events.jsonl, stderr.log), starts the agent through the backend, and
// decodes its output into events for sink until it stops.
func (h *Harness) startProcess(ctx context.Context, req core.Request, started time.Time, sink core.Sink) (*process, error) {
	runDir := h.layout.runDir(req.RunID)
	for _, dir := range []string{runDir, h.layout.sessionsDir(), h.layout.logsDir()} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("failed to create state directory: %w", err)
		}
	}
	request, err := json.Marshal(requestToDTO(req))
	if err != nil {
		return nil, fmt.Errorf("failed to encode runner request: %w", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, RequestFile), request, 0o600); err != nil {
		return nil, fmt.Errorf("failed to write request: %w", err)
	}
	if err := h.layout.saveSummary(core.Result{Request: req, Status: core.StatusRunning, StartedAt: started}); err != nil {
		return nil, err
	}
	p := &process{h: h, sessionFile: h.layout.sessionFile(req.SessionID), opsDir: h.layout.operationsDir(req.SessionID)}
	if p.events, err = os.Create(filepath.Join(runDir, EventsFile)); err != nil {
		return nil, fmt.Errorf("failed to create events file: %w", err)
	}
	if p.stderr, err = os.Create(filepath.Join(runDir, StderrFile)); err != nil {
		_ = p.events.Close()

		return nil, fmt.Errorf("failed to create stderr file: %w", err)
	}

	pr, pw, err := os.Pipe()
	if err != nil {
		p.closeFiles()

		return nil, fmt.Errorf("failed to create output pipe: %w", err)
	}
	p.pipe = pr
	agent, err := h.backend.Start(ctx, Launch{
		Request: req, RunnerRequest: request,
		SessionsDir: h.layout.sessionsDir(), LogsDir: h.layout.logsDir(),
		Stdout: pw, Stderr: p.stderr,
	})
	if err != nil {
		_ = pw.Close()
		_ = pr.Close()
		p.closeFiles()
		failed := core.Result{Request: req, Status: core.StatusFailed, StartedAt: started, RunnerExitCode: -1}
		failed.Stats.Errors = []string{err.Error()}

		return nil, errors.Join(err, h.layout.saveSummary(failed))
	}
	p.agent = agent
	p.log = h.log.With(slog.String("run_id", req.RunID))
	p.log.LogAttrs(ctx, slog.LevelDebug, "agent started")

	p.readDone = make(chan struct{})
	go p.read(ctx, sink)

	return p, nil
}

func (p *process) read(ctx context.Context, sink core.Sink) {
	defer close(p.readDone)
	decoder := NewDecoder()
	br := bufio.NewReaderSize(p.pipe, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			if _, werr := p.events.Write(line); werr != nil {
				p.log.LogAttrs(ctx, slog.LevelWarn, "event write failed", slog.String("error", werr.Error()))
			}
			for _, e := range decoder.Decode(line) {
				sink(e)
			}
		}
		if err != nil {
			return
		}
	}
}

// supervise waits for the agent to stop and stops it on an interrupt, a kill,
// the end of ctx (timeout or cancellation), or a disk overrun.
func (p *process) supervise(ctx context.Context, interrupt, kill <-chan struct{}) core.Termination {
	disk := time.NewTicker(diskPollInterval)
	defer disk.Stop()
	for {
		select {
		case <-p.agent.Done():
			return core.TerminationExited
		case <-interrupt:
			p.log.LogAttrs(ctx, slog.LevelInfo, "stopping agent", slog.String("reason", "interrupt"))
			p.stopGracefully()

			return core.TerminationInterrupted
		case <-kill:
			p.log.LogAttrs(ctx, slog.LevelInfo, "stopping agent", slog.String("reason", "kill"))
			p.teardown()

			return core.TerminationInterrupted
		case <-ctx.Done():
			cause := context.Cause(ctx)
			p.log.LogAttrs(ctx, slog.LevelInfo, "stopping agent", slog.String("reason", cause.Error()))
			p.stopGracefully()
			if errors.Is(cause, errTimedOut) {
				return core.TerminationTimeout
			}

			return core.TerminationInterrupted
		case <-disk.C:
			if p.h.cfg.MaxDisk <= 0 {
				continue
			}
			if used := dirSize(p.opsDir); used > p.h.cfg.MaxDisk {
				p.log.LogAttrs(ctx, slog.LevelInfo, "stopping agent",
					slog.String("reason", "disk limit"),
					slog.Int64("used_bytes", used),
					slog.Int64("limit_bytes", p.h.cfg.MaxDisk))
				p.teardown()

				return core.TerminationDiskLimit
			}
		}
	}
}

// stopGracefully interrupts the agent so it can record its state and stop
// its tools, then falls back to teardown after the grace period.
func (p *process) stopGracefully() {
	p.agent.Interrupt()
	select {
	case <-p.agent.Done():
	case <-time.After(p.h.cfg.KillGrace):
		p.teardown()
	}
}

// teardown stops the agent and every still-running background tool group:
// SIGTERM, a grace period, then SIGKILL.
func (p *process) teardown() {
	groups := liveOperationGroups(p.sessionFile)
	p.agent.Terminate()
	signalGroups(groups, syscall.SIGTERM)
	select {
	case <-p.agent.Done():
	case <-time.After(p.h.cfg.KillGrace):
		p.agent.Kill()
		signalGroups(groups, syscall.SIGKILL)
		<-p.agent.Done()
	}
}

// finish reaps anything the agent left behind, drains its output, and closes the run files.
func (p *process) finish(ctx context.Context, termination core.Termination) processExit {
	<-p.agent.Done()
	// The agent is gone. Its process group and the tools it still records as
	// running are orphans; nothing of this run may outlive it.
	if orphans := liveOperationGroups(p.sessionFile); len(orphans) > 0 {
		p.log.LogAttrs(ctx, slog.LevelInfo, "killing orphaned tools", slog.Int("groups", len(orphans)))
		signalGroups(orphans, syscall.SIGKILL)
	}
	p.agent.Kill()

	select {
	case <-p.readDone:
	case <-time.After(drainTimeout):
		_ = p.pipe.Close()
		<-p.readDone
	}
	_ = p.pipe.Close()
	p.closeFiles()

	return processExit{code: p.agent.ExitCode(), termination: termination, endedAt: time.Now()}
}

func (p *process) closeFiles() {
	if p.events != nil {
		_ = p.events.Close()
	}
	if p.stderr != nil {
		_ = p.stderr.Close()
	}
}

func signalGroups(groups []int, sig syscall.Signal) {
	for _, g := range groups {
		if g > 1 {
			_ = syscall.Kill(-g, sig) // ESRCH means the group is already gone
		}
	}
}

// liveOperationGroups returns process groups of operations whose latest
// recorded status in the session file is not terminal.
func liveOperationGroups(sessionFile string) []int {
	f, err := os.Open(sessionFile)
	if err != nil {
		return nil
	}
	defer f.Close()
	type opState struct {
		status string
		pgid   int
	}
	latest := map[string]opState{}
	br := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		var rec sessionRecordDTO
		if json.Unmarshal(line, &rec) == nil && rec.Type == "operation" {
			op := rec.Data.Operation
			state := latest[op.ID]
			state.status = op.Status
			if op.State.ProcessGroupID != 0 {
				state.pgid = op.State.ProcessGroupID
			}
			latest[op.ID] = state
		}
		if err != nil {
			break
		}
	}
	var groups []int
	for _, s := range latest {
		if s.pgid > 1 && !isTerminal(s.status) {
			groups = append(groups, s.pgid)
		}
	}

	return groups
}

func dirSize(dir string) int64 {
	var size int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // unreadable entries don't count toward the limit
		}
		if info, err := d.Info(); err == nil && info.Mode().IsRegular() {
			size += info.Size()
		}

		return nil
	})

	return size
}
