package domain

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrPreflightBlocked means a blocking preflight finding stopped the run before it started.
var ErrPreflightBlocked = errors.New("preflight blocked the run")

// errRunTimedOut is the cancellation cause set when RunRequest.Timeout expires.
var errRunTimedOut = errors.New("run timed out")

// RunService runs one task: preflight, runner, stats, and persistence.
type RunService interface {
	// Run returns a result for every run that started, whatever its Status.
	// It returns an error only when the run could not start or be saved.
	Run(ctx context.Context, req RunRequest, sink EventSink) (RunResult, error)
}

type runService struct {
	preflight Preflight
	runner    Runner
	store     RunStore
}

func NewRunService(preflight Preflight, runner Runner, store RunStore) RunService {
	return &runService{preflight: preflight, runner: runner, store: store}
}

func (s *runService) Run(ctx context.Context, req RunRequest, sink EventSink) (RunResult, error) {
	findings, err := s.preflight.Check(ctx, req)
	if err != nil {
		return RunResult{}, fmt.Errorf("failed to run preflight checks: %w", err)
	}
	var blocking []string
	var warnings []Finding
	for _, f := range findings {
		allowed := f.Code == FindingDotenvRisky && req.AllowDotenv
		if f.Severity == SeverityBlocking && !allowed {
			blocking = append(blocking, f.Message)

			continue
		}
		warnings = append(warnings, f)
	}
	if len(blocking) > 0 {
		return RunResult{}, fmt.Errorf("%w: %s", ErrPreflightBlocked, strings.Join(blocking, "; "))
	}

	started := time.Now()
	if req.SessionID == "" {
		req.SessionID = uuid.NewString()
	}
	if req.RunID == "" {
		req.RunID = started.Format("20060102-150405") + "-" + req.SessionID[:min(8, len(req.SessionID))]
	}

	collector := NewStatsCollector()
	emit := func(e Event) {
		collector.Add(e)
		sink.Emit(e)
	}
	emit(RunStarted{
		At: started, RunID: req.RunID, SessionID: req.SessionID,
		Provider: req.Provider, Model: req.Model, Effort: req.Effort, Workspace: req.Workspace,
	})
	for _, w := range warnings {
		emit(PreflightWarning{At: started, Code: w.Code, Message: w.Message})
	}

	runCtx, cancel := context.WithCancel(ctx)
	if req.Timeout > 0 {
		runCtx, cancel = context.WithTimeoutCause(ctx, req.Timeout, errRunTimedOut)
	}
	defer cancel()

	exit, err := s.runner.Run(runCtx, req, emit)
	if err != nil {
		return RunResult{}, fmt.Errorf("failed to run runner: %w", err)
	}
	collector.Close(exit.EndedAt)

	result := RunResult{
		Request:        req,
		Status:         classify(runCtx, exit, collector),
		RunnerExitCode: exit.Code,
		StartedAt:      started,
		Wall:           exit.EndedAt.Sub(started),
		Stats:          collector.Stats(),
		Answer:         collector.Answer(),
	}
	if err := s.store.Save(ctx, result); err != nil {
		return result, fmt.Errorf("failed to save run: %w", err)
	}
	sink.Emit(RunFinished{At: exit.EndedAt, Result: result})

	return result, nil
}

func classify(runCtx context.Context, exit RunnerExit, collector *StatsCollector) Status {
	switch exit.Termination {
	case TerminationDiskLimit:
		return StatusDiskLimit
	case TerminationCanceled:
		if errors.Is(context.Cause(runCtx), errRunTimedOut) {
			return StatusTimeout
		}

		return StatusInterrupted
	case TerminationExited:
	}
	if exit.Code != 0 || collector.HasError() {
		return StatusFailed
	}

	return StatusOK
}
