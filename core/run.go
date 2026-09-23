// Package core is the pure part of uagent: what a run is, what happens during
// it, and the rules that turn events into statistics and an outcome. It has no
// I/O and depends only on the standard library.
package core

import "time"

// UserInput is one user message with the ID the runner deduplicates on.
type UserInput struct {
	ID   string
	Text string
}

// Request is one task for unreal-agent-runner plus the guards around it.
// Set either Prompt or Messages.
type Request struct {
	RunID     string
	SessionID string
	Prompt    string
	// Messages are delivered in order with their IDs, so the runner can
	// acknowledge each one and ignore repeats.
	Messages        []UserInput
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

// Result is a finished run.
type Result struct {
	Request        Request
	Status         Status
	RunnerExitCode int
	StartedAt      time.Time
	Wall           time.Duration
	Stats          Stats
	Answer         string
}

// Status is the outcome of a finished run.
type Status string

const (
	// StatusRunning marks a run that has started and not finished. A run
	// record that stays running means uagent stopped before the run ended.
	StatusRunning     Status = "running"
	StatusOK          Status = "ok"
	StatusFailed      Status = "error"
	StatusTimeout     Status = "timeout"
	StatusInterrupted Status = "interrupted"
	StatusDiskLimit   Status = "disk_limit"
)

// Termination says why the runner process stopped.
type Termination string

const (
	// TerminationExited means the runner exited on its own.
	TerminationExited Termination = "exited"
	// TerminationTimeout means the run's timeout expired and the runner was killed.
	TerminationTimeout Termination = "timeout"
	// TerminationInterrupted means the caller canceled the run and the runner was killed.
	TerminationInterrupted Termination = "interrupted"
	// TerminationDiskLimit means tool output grew past the disk limit and the runner was killed.
	TerminationDiskLimit Termination = "disk_limit"
)

// Classify decides a run's status from how the runner stopped, its exit code,
// and whether it reported an error or a model failure.
func Classify(termination Termination, exitCode int, reportedError bool) Status {
	switch termination {
	case TerminationTimeout:
		return StatusTimeout
	case TerminationInterrupted:
		return StatusInterrupted
	case TerminationDiskLimit:
		return StatusDiskLimit
	case TerminationExited:
	}
	if exitCode != 0 || reportedError {
		return StatusFailed
	}

	return StatusOK
}

// NewRunID names a run directory: YYYYMMDD-HHMMSS-<first 8 characters of the session ID>.
func NewRunID(started time.Time, sessionID string) string {
	return started.Format("20060102-150405") + "-" + sessionID[:min(8, len(sessionID))]
}
