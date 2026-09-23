// Package harness runs unreal-agent-runner with uagent's guards: preflight
// checks, a pinned environment, process-group supervision, a disk limit, and
// run records in the state directory. The CLI and the TUI both call it.
package harness

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
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
	// RunnerPath is the unreal-agent-runner executable; see FindRunner.
	RunnerPath string
	// StateDir holds sessions, logs, and run records; see DefaultStateDir.
	StateDir string
	// MaxDisk kills a run when its tool output exceeds this many bytes (0 disables).
	MaxDisk int64
	// KillGrace is the wait between SIGTERM and SIGKILL (default 5s).
	KillGrace time.Duration
	// Logger receives diagnostics (default slog.Default()).
	Logger *slog.Logger
	// Getenv reads the environment for credential checks (default os.Getenv).
	Getenv func(string) string
}

// Harness runs tasks and reads saved runs. It is safe for concurrent runs.
type Harness struct {
	cfg    Config
	layout layout
	log    *slog.Logger
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

	return &Harness{cfg: cfg, layout: layout{root: cfg.StateDir}, log: cfg.Logger.WithGroup("harness")}
}

// Run executes one task: preflight, runner, statistics, and summary.json.
//
// It returns an error wrapping ErrPreflightBlocked when preflight blocks the
// run, and an error when the runner cannot start or the summary cannot be
// saved. A run that started always produces a Result whose Status says how it
// ended, and sink always receives RunStarted first and RunFinished last.
func (h *Harness) Run(ctx context.Context, req core.Request, sink core.Sink) (core.Result, error) {
	findings, err := h.Preflight(req)
	if err != nil {
		return core.Result{}, err
	}
	blocking, warnings := core.Triage(findings, req.AllowDotenv)
	if len(blocking) > 0 {
		return core.Result{}, fmt.Errorf("%w: %s", ErrPreflightBlocked, core.Messages(blocking))
	}

	started := time.Now()
	if req.SessionID == "" {
		req.SessionID = uuid.NewString()
	}
	if req.RunID == "" {
		req.RunID = core.NewRunID(started, req.SessionID)
	}
	collector := core.NewStatsCollector()
	emit := func(e core.Event) {
		collector.Add(e)
		sink(e)
	}
	emit(core.RunStarted{
		At: started, RunID: req.RunID, SessionID: req.SessionID,
		Provider: req.Provider, Model: req.Model, Effort: req.Effort, Workspace: req.Workspace,
	})
	for _, w := range warnings {
		emit(core.PreflightWarning{At: started, Code: w.Code, Message: w.Message})
	}

	runCtx, cancel := context.WithCancel(ctx)
	if req.Timeout > 0 {
		runCtx, cancel = context.WithTimeoutCause(ctx, req.Timeout, errTimedOut)
	}
	defer cancel()
	exit, err := h.runProcess(runCtx, req, emit)
	if err != nil {
		return core.Result{}, err
	}
	collector.Close(exit.endedAt)

	result := core.Result{
		Request:        req,
		Status:         core.Classify(exit.termination, exit.code, collector.HasError()),
		RunnerExitCode: exit.code,
		StartedAt:      started,
		Wall:           exit.endedAt.Sub(started),
		Stats:          collector.Stats(),
		Answer:         collector.Answer(),
	}
	saveErr := h.layout.saveSummary(result)
	sink(core.RunFinished{At: exit.endedAt, Result: result})
	if saveErr != nil {
		return result, saveErr
	}

	return result, nil
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
