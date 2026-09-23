package harness

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// process is one running unreal-agent-runner.
type process struct {
	h           *Harness
	cmd         *exec.Cmd
	pgid        int
	log         *slog.Logger
	sessionFile string
	opsDir      string
	pipe        *os.File
	events      *os.File
	stderr      *os.File
	readDone    chan struct{}
	waitCh      chan struct{}
}

// startProcess writes the run records (request.json, a running summary.json,
// events.jsonl, stderr.log), starts the runner in its own process group, and
// decodes its stdout into events for sink until it exits.
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

	// A raw pipe instead of StdoutPipe: Wait must not block on grandchildren
	// that inherited stdout, and reading stops on our terms.
	pr, pw, err := os.Pipe()
	if err != nil {
		p.closeFiles()

		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	p.pipe = pr
	cmd := exec.Command(h.cfg.RunnerPath, //nolint:noctx // supervise handles cancellation, including tool process groups
		"-workspace", req.Workspace,
		"-session-directory", h.layout.sessionsDir(),
		"-log-directory", h.layout.logsDir(),
	)
	cmd.Dir = req.Workspace
	cmd.Env = runnerEnv(req)
	cmd.Stdin = bytes.NewReader(request)
	cmd.Stdout, cmd.Stderr = pw, p.stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		_ = pw.Close()
		_ = pr.Close()
		p.closeFiles()
		startErr := fmt.Errorf("failed to start runner: %w", err)
		failed := core.Result{Request: req, Status: core.StatusFailed, StartedAt: started, RunnerExitCode: -1}
		failed.Stats.Errors = []string{startErr.Error()}

		return nil, errors.Join(startErr, h.layout.saveSummary(failed))
	}
	_ = pw.Close()
	p.cmd, p.pgid = cmd, cmd.Process.Pid
	p.log = h.log.With(
		slog.String("run_id", req.RunID),
		slog.Int("pgid", p.pgid),
	)
	p.log.LogAttrs(ctx, slog.LevelDebug, "runner started")

	p.readDone = make(chan struct{})
	go p.read(ctx, sink)
	p.waitCh = make(chan struct{})
	go func() {
		_ = cmd.Wait() // the exit code is read from ProcessState in finish
		close(p.waitCh)
	}()

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

// supervise waits for the runner to exit and stops it on an interrupt, a kill,
// the end of ctx (timeout or cancellation), or a disk overrun.
func (p *process) supervise(ctx context.Context, interrupt, kill <-chan struct{}) core.Termination {
	disk := time.NewTicker(diskPollInterval)
	defer disk.Stop()
	for {
		select {
		case <-p.waitCh:
			return core.TerminationExited
		case <-interrupt:
			p.log.LogAttrs(ctx, slog.LevelInfo, "stopping runner", slog.String("reason", "interrupt"))
			p.stopGracefully()

			return core.TerminationInterrupted
		case <-kill:
			p.log.LogAttrs(ctx, slog.LevelInfo, "stopping runner", slog.String("reason", "kill"))
			p.teardown()

			return core.TerminationInterrupted
		case <-ctx.Done():
			cause := context.Cause(ctx)
			p.log.LogAttrs(ctx, slog.LevelInfo, "stopping runner", slog.String("reason", cause.Error()))
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
				p.log.LogAttrs(ctx, slog.LevelInfo, "stopping runner",
					slog.String("reason", "disk limit"),
					slog.Int64("used_bytes", used),
					slog.Int64("limit_bytes", p.h.cfg.MaxDisk))
				p.teardown()

				return core.TerminationDiskLimit
			}
		}
	}
}

// stopGracefully sends SIGINT to the runner so it can record its state and
// stop its tools, then falls back to teardown after the grace period.
func (p *process) stopGracefully() {
	_ = syscall.Kill(p.pgid, syscall.SIGINT) // ESRCH means it already exited
	select {
	case <-p.waitCh:
	case <-time.After(p.h.cfg.KillGrace):
		p.teardown()
	}
}

// teardown stops the runner's process group and every still-running
// background tool group: SIGTERM, a grace period, then SIGKILL.
func (p *process) teardown() {
	groups := append([]int{p.pgid}, liveOperationGroups(p.sessionFile)...)
	signalGroups(groups, syscall.SIGTERM)
	select {
	case <-p.waitCh:
	case <-time.After(p.h.cfg.KillGrace):
		signalGroups(groups, syscall.SIGKILL)
		<-p.waitCh
	}
}

// finish reaps anything the runner left behind, drains its output, and closes the run files.
func (p *process) finish(ctx context.Context, termination core.Termination) processExit {
	<-p.waitCh
	// The runner is gone. Its process group and the tools it still records as
	// running are orphans; nothing of this run may outlive it.
	if orphans := liveOperationGroups(p.sessionFile); len(orphans) > 0 {
		p.log.LogAttrs(ctx, slog.LevelInfo, "killing orphaned tools", slog.Int("groups", len(orphans)))
		signalGroups(orphans, syscall.SIGKILL)
	}
	signalGroups([]int{p.pgid}, syscall.SIGKILL)

	select {
	case <-p.readDone:
	case <-time.After(drainTimeout):
		_ = p.pipe.Close()
		<-p.readDone
	}
	_ = p.pipe.Close()
	p.closeFiles()

	return processExit{code: p.cmd.ProcessState.ExitCode(), termination: termination, endedAt: time.Now()}
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

// runnerEnv pins provider, model, and base URL. The runner applies workspace
// .env values only to unset variables, so pinning them (even to "") stops a
// workspace .env from overriding them.
func runnerEnv(req core.Request) []string {
	pinned := map[string]string{
		"UNREAL_HARNESS_LLM_PROVIDER": req.Provider,
		"UNREAL_HARNESS_LLM_MODEL":    req.Model,
		"UNREAL_HARNESS_LLM_BASE_URL": req.BaseURL,
	}
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if _, ok := pinned[name]; !ok {
			env = append(env, kv)
		}
	}
	for name, value := range pinned {
		env = append(env, name+"="+value)
	}

	return env
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
