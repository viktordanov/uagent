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

// runProcess starts unreal-agent-runner for req, decodes its stdout into
// events for sink, and stops it with all background tools when ctx ends or
// tool output exceeds the disk limit.
func (h *Harness) runProcess(ctx context.Context, req core.Request, sink core.Sink) (processExit, error) {
	runDir := h.layout.runDir(req.RunID)
	for _, dir := range []string{runDir, h.layout.sessionsDir(), h.layout.logsDir()} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return processExit{}, fmt.Errorf("failed to create state directory: %w", err)
		}
	}
	request, err := json.Marshal(requestToDTO(req))
	if err != nil {
		return processExit{}, fmt.Errorf("failed to encode runner request: %w", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, RequestFile), request, 0o600); err != nil {
		return processExit{}, fmt.Errorf("failed to write request: %w", err)
	}
	events, err := os.Create(filepath.Join(runDir, EventsFile))
	if err != nil {
		return processExit{}, fmt.Errorf("failed to create events file: %w", err)
	}
	defer events.Close()
	stderr, err := os.Create(filepath.Join(runDir, StderrFile))
	if err != nil {
		return processExit{}, fmt.Errorf("failed to create stderr file: %w", err)
	}
	defer stderr.Close()

	// A raw pipe instead of StdoutPipe: Wait must not block on grandchildren
	// that inherited stdout, and reading stops on our terms.
	pr, pw, err := os.Pipe()
	if err != nil {
		return processExit{}, fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	cmd := exec.Command(h.cfg.RunnerPath, //nolint:noctx // teardown handles cancellation, including tool process groups
		"-workspace", req.Workspace,
		"-session-directory", h.layout.sessionsDir(),
		"-log-directory", h.layout.logsDir(),
	)
	cmd.Dir = req.Workspace
	cmd.Env = runnerEnv(req)
	cmd.Stdin = bytes.NewReader(request)
	cmd.Stdout, cmd.Stderr = pw, stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		_ = pw.Close()
		_ = pr.Close()

		return processExit{}, fmt.Errorf("failed to start runner: %w", err)
	}
	_ = pw.Close()
	pgid := cmd.Process.Pid
	log := h.log.With(
		slog.String("run_id", req.RunID),
		slog.Int("pgid", pgid),
	)
	log.LogAttrs(ctx, slog.LevelDebug, "runner started")

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		decoder := NewDecoder()
		br := bufio.NewReaderSize(pr, 1<<20)
		for {
			line, err := br.ReadBytes('\n')
			if len(line) > 0 {
				if _, werr := events.Write(line); werr != nil {
					log.LogAttrs(ctx, slog.LevelWarn, "event write failed", slog.String("error", werr.Error()))
				}
				for _, e := range decoder.Decode(line) {
					sink(e)
				}
			}
			if err != nil {
				return
			}
		}
	}()
	waitCh := make(chan struct{})
	go func() {
		_ = cmd.Wait() // the exit code is read from ProcessState below
		close(waitCh)
	}()

	termination := h.supervise(ctx, req.SessionID, pgid, waitCh, log)

	select {
	case <-readDone:
	case <-time.After(drainTimeout):
		_ = pr.Close()
		<-readDone
	}
	_ = pr.Close()

	return processExit{code: cmd.ProcessState.ExitCode(), termination: termination, endedAt: time.Now()}, nil
}

// supervise waits for the runner, killing it on context end or disk overrun.
func (h *Harness) supervise(ctx context.Context, sessionID string, pgid int, waitCh <-chan struct{}, log *slog.Logger) core.Termination {
	disk := time.NewTicker(diskPollInterval)
	defer disk.Stop()
	sessionFile := h.layout.sessionFile(sessionID)
	for {
		select {
		case <-waitCh:
			// The runner is gone; tools it still records as running are orphans.
			if orphans := liveOperationGroups(sessionFile); len(orphans) > 0 {
				log.LogAttrs(ctx, slog.LevelInfo, "killing orphaned tools", slog.Int("groups", len(orphans)))
				signalGroups(append(orphans, pgid), syscall.SIGKILL)
			}

			return core.TerminationExited
		case <-ctx.Done():
			cause := context.Cause(ctx)
			log.LogAttrs(ctx, slog.LevelInfo, "stopping runner", slog.String("reason", cause.Error()))
			h.teardown(pgid, sessionFile, waitCh)
			if errors.Is(cause, errTimedOut) {
				return core.TerminationTimeout
			}

			return core.TerminationInterrupted
		case <-disk.C:
			if h.cfg.MaxDisk <= 0 {
				continue
			}
			if used := dirSize(h.layout.operationsDir(sessionID)); used > h.cfg.MaxDisk {
				log.LogAttrs(ctx, slog.LevelInfo, "stopping runner",
					slog.String("reason", "disk limit"),
					slog.Int64("used_bytes", used),
					slog.Int64("limit_bytes", h.cfg.MaxDisk))
				h.teardown(pgid, sessionFile, waitCh)

				return core.TerminationDiskLimit
			}
		}
	}
}

// teardown stops the runner's process group and every still-running
// background tool group: SIGTERM, a grace period, then SIGKILL.
func (h *Harness) teardown(pgid int, sessionFile string, waitCh <-chan struct{}) {
	groups := append([]int{pgid}, liveOperationGroups(sessionFile)...)
	signalGroups(groups, syscall.SIGTERM)
	select {
	case <-waitCh:
	case <-time.After(h.cfg.KillGrace):
		signalGroups(groups, syscall.SIGKILL)
		<-waitCh
	}
	// Tool groups may outlive the runner.
	signalGroups(append(groups, liveOperationGroups(sessionFile)...), syscall.SIGKILL)
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
