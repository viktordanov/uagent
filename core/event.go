package core

import "time"

// Event is one normalized thing that happened during a run.
type Event interface {
	OccurredAt() time.Time
}

// Sink receives events as they happen, from one goroutine at a time.
type Sink func(Event)

type RunStarted struct {
	At        time.Time
	RunID     string
	SessionID string
	Provider  string
	Model     string
	Effort    string
	Workspace string
}

type PreflightWarning struct {
	At      time.Time
	Code    string
	Message string
}

type TurnStarted struct {
	At   time.Time
	Turn int
}

type ModelResponded struct {
	At       time.Time
	Turn     int
	Duration time.Duration
	Usage    Tokens
	// Stop is empty or "complete" for a normal response.
	Stop    string
	Failure string
}

type ToolCalled struct {
	At     time.Time
	CallID string
	Name   string
	Label  string
}

type ToolStarted struct {
	At     time.Time
	CallID string
	OpID   string
	Name   string
	Label  string
}

// ToolFinished ends a tool operation. OpID is empty when the call failed
// before any operation started.
type ToolFinished struct {
	At       time.Time
	CallID   string
	OpID     string
	Name     string
	Label    string
	OK       bool
	Detail   string
	Duration time.Duration
}

type AssistantMessage struct {
	At    time.Time
	Text  string
	Final bool
}

type ReasoningSummary struct {
	At   time.Time
	Text string
}

type RunnerError struct {
	At      time.Time
	Message string
}

type RunFinished struct {
	At     time.Time
	Result Result
}

func (e RunStarted) OccurredAt() time.Time       { return e.At }
func (e PreflightWarning) OccurredAt() time.Time { return e.At }
func (e TurnStarted) OccurredAt() time.Time      { return e.At }
func (e ModelResponded) OccurredAt() time.Time   { return e.At }
func (e ToolCalled) OccurredAt() time.Time       { return e.At }
func (e ToolStarted) OccurredAt() time.Time      { return e.At }
func (e ToolFinished) OccurredAt() time.Time     { return e.At }
func (e AssistantMessage) OccurredAt() time.Time { return e.At }
func (e ReasoningSummary) OccurredAt() time.Time { return e.At }
func (e RunnerError) OccurredAt() time.Time      { return e.At }
func (e RunFinished) OccurredAt() time.Time      { return e.At }
