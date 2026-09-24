// Package harness runs unreal-agent-runner with uagent's guards: preflight
// checks, a session lock, a pinned environment, process-group supervision, a
// disk limit, and run records in the state directory. The CLI and the TUI both
// call it.
package harness

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/viktordanov/uagent/core"
)

// RunnerName is the runner executable uagent looks for.
const RunnerName = "unreal-agent-runner"

const defaultKillGrace = 5 * time.Second

// ErrPreflightBlocked means a blocking preflight finding stopped the run before it started.
var ErrPreflightBlocked = errors.New("preflight blocked the run")

// errTimedOut is the cancellation cause when Request.Timeout expires.
var errTimedOut = errors.New("run timed out")

// Config configures a Harness.
type Config struct {
	// RunnerPath is the unreal-agent-runner executable; see FindRunner. It is
	// used when Backend is nil.
	RunnerPath string
	// Backend runs the agent (default: RunnerBackend with RunnerPath).
	Backend Backend
	// StateDir holds sessions, logs, and run records; see DefaultStateDir.
	StateDir string
	// MaxDisk kills a run when its tool output exceeds this many bytes (0 disables).
	MaxDisk int64
	// KillGrace is how long a stopping runner gets before it is killed (default 5s).
	KillGrace time.Duration
	// Logger receives diagnostics (default slog.Default()).
	Logger *slog.Logger
	// Getenv reads the environment for credential checks (default os.Getenv).
	Getenv func(string) string
}

// Harness runs tasks and reads saved runs. It is safe for concurrent runs.
type Harness struct {
	cfg     Config
	backend Backend
	layout  layout
	log     *slog.Logger
}

func New(cfg Config) *Harness {
	if cfg.KillGrace <= 0 {
		cfg.KillGrace = defaultKillGrace
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Getenv == nil {
		cfg.Getenv = os.Getenv
	}

	backend := cfg.Backend
	if backend == nil {
		backend = RunnerBackend{Path: cfg.RunnerPath}
	}

	return &Harness{cfg: cfg, backend: backend, layout: layout{root: cfg.StateDir}, log: cfg.Logger.WithGroup("harness")}
}

// Run executes one task and waits for it: Start followed by Wait.
func (h *Harness) Run(ctx context.Context, req core.Request, sink core.Sink) (core.Result, error) {
	run, err := h.Start(ctx, req, sink)
	if err != nil {
		return core.Result{}, err
	}

	return run.Wait()
}

// Start checks the request, takes the session lock, starts the runner, and
// returns once it is running.
//
// It returns an error wrapping ErrPreflightBlocked when preflight blocks the
// run, ErrSessionBusy when another run holds the session, and an error when
// the request is invalid or the runner cannot start. Once Start succeeds, sink
// receives RunStarted first and RunFinished last, and Wait returns a Result
// whose Status says how the run ended.
func (h *Harness) Start(ctx context.Context, req core.Request, sink core.Sink) (*Run, error) {
	req, err := normalizeRequest(req)
	if err != nil {
		return nil, err
	}
	findings, err := h.Preflight(req)
	if err != nil {
		return nil, err
	}
	blocking, warnings := core.Triage(findings, req.AllowDotenv)
	if len(blocking) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrPreflightBlocked, core.Messages(blocking))
	}

	started := time.Now()
	if req.SessionID == "" {
		req.SessionID = uuid.NewString()
	}
	unlock, err := LockSession(h.cfg.StateDir, req.SessionID)
	if err != nil {
		return nil, err
	}
	// A run that ended without cleaning up (uagent or uah was killed) leaves
	// its tools running and recorded as live. The lock means no run owns
	// them now, and the runner would only mark them failed on resume.
	if orphans := liveOperationGroups(h.layout.sessionFile(req.SessionID)); len(orphans) > 0 {
		h.log.LogAttrs(ctx, slog.LevelInfo, "killing tools left by an earlier run",
			slog.String("session_id", req.SessionID),
			slog.Int("groups", len(orphans)))
		signalGroups(orphans, syscall.SIGKILL)
	}
	if req.RunID == "" {
		// Runs can start within the same second: unusedRunID reserves a
		// free name by creating its directory.
		req.RunID = h.layout.unusedRunID(core.NewRunID(started, req.SessionID))
	}

	runCtx, cancel := context.WithCancel(ctx)
	if req.Timeout > 0 {
		runCtx, cancel = context.WithTimeoutCause(ctx, req.Timeout, errTimedOut)
	}
	r := &Run{
		h: h, req: req, started: started, sink: sink, collector: core.NewStatsCollector(),
		ctx: runCtx, cancel: cancel, unlock: unlock,
		interrupt: make(chan struct{}), kill: make(chan struct{}), done: make(chan struct{}),
	}
	r.emit(core.RunStarted{
		At: started, RunID: req.RunID, SessionID: req.SessionID,
		Provider: req.Provider, Model: req.Model, Effort: req.Effort, Workspace: req.Workspace,
	})
	for _, w := range warnings {
		r.emit(core.PreflightWarning{At: started, Code: w.Code, Message: w.Message})
	}

	proc, err := h.startProcess(runCtx, req, started, r.emit)
	if err != nil {
		cancel()
		if uerr := unlock(); uerr != nil {
			err = errors.Join(err, uerr)
		}

		return nil, err
	}
	r.proc = proc
	go r.supervise()

	return r, nil
}

// normalizeRequest checks that exactly one of Prompt and Messages is set and
// gives every message an ID.
func normalizeRequest(req core.Request) (core.Request, error) {
	hasPrompt := strings.TrimSpace(req.Prompt) != ""
	switch {
	case hasPrompt && len(req.Messages) > 0:
		return req, errors.New("failed to start run: set either a prompt or messages, not both")
	case !hasPrompt && len(req.Messages) == 0:
		return req, errors.New("failed to start run: the request has no prompt or messages")
	}
	messages := make([]core.UserInput, len(req.Messages))
	for i, m := range req.Messages {
		if strings.TrimSpace(m.Text) == "" {
			return req, fmt.Errorf("failed to start run: message %d is empty", i+1)
		}
		if m.ID == "" {
			m.ID = uuid.NewString()
		}
		messages[i] = m
	}
	if len(messages) > 0 {
		req.Messages = messages
	}

	return req, nil
}

// Preflight checks the workspace, state directory, workspace .env, and
// provider credentials for req without running anything. Use core.Triage to
// split the findings into blocking and warnings.
func (h *Harness) Preflight(req core.Request) ([]core.Finding, error) {
	c := checker{stateDir: h.cfg.StateDir, getenv: h.cfg.Getenv}
	findings, err := c.check(req)
	if err != nil {
		return nil, fmt.Errorf("failed to run preflight checks: %w", err)
	}

	return findings, nil
}

// FindRunner finds unreal-agent-runner: an explicit path, then ~/.local/bin, then PATH.
func FindRunner(explicit string) (string, error) {
	if explicit != "" {
		if !isExecutable(explicit) {
			return "", fmt.Errorf("failed to use runner %q: not an executable file", explicit)
		}

		return explicit, nil
	}
	if home, err := os.UserHomeDir(); err == nil {
		if p := filepath.Join(home, ".local", "bin", RunnerName); isExecutable(p) {
			return p, nil
		}
	}
	p, err := exec.LookPath(RunnerName)
	if err != nil {
		return "", fmt.Errorf("failed to find %s (install: GOBIN=\"$HOME/.local/bin\" go install github.com/unreallabsai/unreal-agent/cmd/unreal-agent-runner@latest): %w", RunnerName, err)
	}

	return p, nil
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)

	return err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0
}
