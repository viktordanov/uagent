package domain

import (
	"context"
	"time"
)

// Status is the outcome of a finished run.
type Status string

const (
	StatusOK          Status = "ok"
	StatusFailed      Status = "error"
	StatusTimeout     Status = "timeout"
	StatusInterrupted Status = "interrupted"
	StatusDiskLimit   Status = "disk_limit"
)

// RunRequest is one task for the runner plus the guards around it.
type RunRequest struct {
	RunID           string
	SessionID       string
	Prompt          string
	Provider        string
	Model           string
	Effort          string
	BaseURL         string
	SystemPrompt    string
	DisallowedTools []string
	MaxAttempts     int
	Workspace       string
	Timeout         time.Duration
	AllowDotenv     bool
}

// RunResult is a finished run: its request, outcome, and aggregated stats.
type RunResult struct {
	Request        RunRequest
	Status         Status
	RunnerExitCode int
	StartedAt      time.Time
	Wall           time.Duration
	Stats          Stats
	Answer         string
}

// Termination says why the runner process stopped.
type Termination string

const (
	// TerminationExited means the runner exited on its own.
	TerminationExited Termination = "exited"
	// TerminationCanceled means the run context ended (timeout or interrupt) and the runner was killed.
	TerminationCanceled Termination = "canceled"
	// TerminationDiskLimit means tool output grew past the disk limit and the runner was killed.
	TerminationDiskLimit Termination = "disk_limit"
)

// RunnerExit describes how the runner process ended.
type RunnerExit struct {
	Code        int
	Termination Termination
	EndedAt     time.Time
}

// Runner executes one request and emits normalized events while it runs.
// It must stop the runner and all of its tools when ctx ends.
type Runner interface {
	Run(ctx context.Context, req RunRequest, emit func(Event)) (RunnerExit, error)
}

// RunStore persists finished runs.
type RunStore interface {
	Save(ctx context.Context, result RunResult) error
}
