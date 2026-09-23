package stream

import (
	"time"

	"github.com/viktordanov/uagent/core"
)

// SchemaVersion is bumped on any breaking change to the event or summary shape.
const SchemaVersion = 1

type header struct {
	V    int       `json:"v"`
	Type string    `json:"type"`
	At   time.Time `json:"at"`
}

func newHeader(eventType string, at time.Time) header {
	return header{V: SchemaVersion, Type: eventType, At: at.UTC()}
}

type RunStartedDTO struct {
	header

	RunID     string `json:"run_id"`
	SessionID string `json:"session_id"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	Effort    string `json:"effort"`
	Workspace string `json:"workspace"`
}

type PreflightWarningDTO struct {
	header

	Code    string `json:"code"`
	Message string `json:"message"`
}

type TurnStartedDTO struct {
	header

	Turn int `json:"turn"`
}

type ModelRespondedDTO struct {
	header

	Turn       int       `json:"turn"`
	DurationMS int64     `json:"duration_ms"`
	Usage      TokensDTO `json:"usage"`
	Stop       string    `json:"stop,omitempty"`
	Failure    string    `json:"failure,omitempty"`
}

type ToolCalledDTO struct {
	header

	CallID string `json:"call_id"`
	Name   string `json:"name"`
	Label  string `json:"label"`
}

type ToolStartedDTO struct {
	header

	CallID string `json:"call_id"`
	OpID   string `json:"op_id"`
	Name   string `json:"name"`
	Label  string `json:"label"`
}

type ToolFinishedDTO struct {
	header

	CallID     string `json:"call_id"`
	OpID       string `json:"op_id,omitempty"`
	Name       string `json:"name"`
	Label      string `json:"label"`
	OK         bool   `json:"ok"`
	Detail     string `json:"detail"`
	DurationMS int64  `json:"duration_ms"`
}

type AssistantMessageDTO struct {
	header

	Text  string `json:"text"`
	Final bool   `json:"final"`
}

type ReasoningDTO struct {
	header

	Text string `json:"text"`
}

type RunnerErrorDTO struct {
	header

	Message string `json:"message"`
}

type RunFinishedDTO struct {
	header

	Summary SummaryDTO `json:"summary"`
}

type TokensDTO struct {
	Input           int64 `json:"input"`
	CachedInput     int64 `json:"cached_input"`
	CacheWriteInput int64 `json:"cache_write_input"`
	Output          int64 `json:"output"`
	Reasoning       int64 `json:"reasoning"`
}

type StatsDTO struct {
	EventSpanMS        int64          `json:"event_span_ms"`
	ModelMS            int64          `json:"model_ms"`
	ToolBusyMS         int64          `json:"tool_busy_ms"`
	ToolModelOverlapMS int64          `json:"tool_model_overlap_ms"`
	Turns              int            `json:"turns"`
	ModelResponses     int            `json:"model_responses"`
	ToolCalls          int            `json:"tool_calls"`
	FailedToolCalls    int            `json:"failed_tool_calls"`
	MaxParallelTools   int            `json:"max_parallel_tools"`
	ToolsByName        map[string]int `json:"tools_by_name"`
	Tokens             TokensDTO      `json:"tokens"`
	StopReasons        map[string]int `json:"stop_reasons,omitempty"`
	Failures           []string       `json:"failures,omitempty"`
	Errors             []string       `json:"errors,omitempty"`
	Warnings           []string       `json:"warnings,omitempty"`
	FinalAnswer        bool           `json:"final_answer"`
}

// SummaryDTO is a finished run. It is the run_finished payload and the content of summary.json.
type SummaryDTO struct {
	V              int       `json:"v"`
	Status         string    `json:"status"`
	RunnerExitCode int       `json:"runner_exit_code"`
	RunID          string    `json:"run_id"`
	SessionID      string    `json:"session_id"`
	Provider       string    `json:"provider"`
	Model          string    `json:"model"`
	Effort         string    `json:"effort"`
	Workspace      string    `json:"workspace"`
	StartedAt      time.Time `json:"started_at"`
	WallMS         int64     `json:"wall_ms"`
	Stats          StatsDTO  `json:"stats"`
	Answer         string    `json:"answer"`
}

// EventToDTO maps a domain event to its wire form. ok is false for unknown events.
func EventToDTO(event core.Event) (dto any, ok bool) {
	switch e := event.(type) {
	case core.RunStarted:
		return RunStartedDTO{
			header: newHeader("run_started", e.At), RunID: e.RunID, SessionID: e.SessionID,
			Provider: e.Provider, Model: e.Model, Effort: e.Effort, Workspace: e.Workspace,
		}, true
	case core.PreflightWarning:
		return PreflightWarningDTO{header: newHeader("preflight_warning", e.At), Code: e.Code, Message: e.Message}, true
	case core.TurnStarted:
		return TurnStartedDTO{header: newHeader("turn_started", e.At), Turn: e.Turn}, true
	case core.ModelResponded:
		return ModelRespondedDTO{
			header: newHeader("model_responded", e.At), Turn: e.Turn, DurationMS: e.Duration.Milliseconds(),
			Usage: tokensToDTO(e.Usage), Stop: e.Stop, Failure: e.Failure,
		}, true
	case core.ToolCalled:
		return ToolCalledDTO{header: newHeader("tool_called", e.At), CallID: e.CallID, Name: e.Name, Label: e.Label}, true
	case core.ToolStarted:
		return ToolStartedDTO{header: newHeader("tool_started", e.At), CallID: e.CallID, OpID: e.OpID, Name: e.Name, Label: e.Label}, true
	case core.ToolFinished:
		return ToolFinishedDTO{
			header: newHeader("tool_finished", e.At), CallID: e.CallID, OpID: e.OpID, Name: e.Name, Label: e.Label,
			OK: e.OK, Detail: e.Detail, DurationMS: e.Duration.Milliseconds(),
		}, true
	case core.AssistantMessage:
		return AssistantMessageDTO{header: newHeader("assistant_message", e.At), Text: e.Text, Final: e.Final}, true
	case core.ReasoningSummary:
		return ReasoningDTO{header: newHeader("reasoning", e.At), Text: e.Text}, true
	case core.RunnerError:
		return RunnerErrorDTO{header: newHeader("runner_error", e.At), Message: e.Message}, true
	case core.RunFinished:
		return RunFinishedDTO{header: newHeader("run_finished", e.At), Summary: SummaryToDTO(e.Result)}, true
	}

	return nil, false
}

func SummaryToDTO(r core.Result) SummaryDTO {
	s := r.Stats

	return SummaryDTO{
		V:              SchemaVersion,
		Status:         string(r.Status),
		RunnerExitCode: r.RunnerExitCode,
		RunID:          r.Request.RunID,
		SessionID:      r.Request.SessionID,
		Provider:       r.Request.Provider,
		Model:          r.Request.Model,
		Effort:         r.Request.Effort,
		Workspace:      r.Request.Workspace,
		StartedAt:      r.StartedAt.UTC(),
		WallMS:         r.Wall.Milliseconds(),
		Answer:         r.Answer,
		Stats: StatsDTO{
			EventSpanMS:        s.EventSpan.Milliseconds(),
			ModelMS:            s.ModelTime.Milliseconds(),
			ToolBusyMS:         s.ToolBusyTime.Milliseconds(),
			ToolModelOverlapMS: s.ToolModelOverlap.Milliseconds(),
			Turns:              s.Turns,
			ModelResponses:     s.ModelResponses,
			ToolCalls:          s.ToolCalls,
			FailedToolCalls:    s.FailedToolCalls,
			MaxParallelTools:   s.MaxParallelTools,
			ToolsByName:        s.ToolsByName,
			Tokens:             tokensToDTO(s.Tokens),
			StopReasons:        s.StopReasons,
			Failures:           s.Failures,
			Errors:             s.Errors,
			Warnings:           s.Warnings,
			FinalAnswer:        s.FinalAnswer,
		},
	}
}

// SummaryFromDTO restores run metadata from a saved summary. Stats are not
// restored: callers recompute them from events.jsonl.
func SummaryFromDTO(d SummaryDTO) core.Result {
	return core.Result{
		Request: core.Request{
			RunID: d.RunID, SessionID: d.SessionID, Provider: d.Provider,
			Model: d.Model, Effort: d.Effort, Workspace: d.Workspace,
		},
		Status:         core.Status(d.Status),
		RunnerExitCode: d.RunnerExitCode,
		StartedAt:      d.StartedAt,
		Wall:           time.Duration(d.WallMS) * time.Millisecond,
		Answer:         d.Answer,
	}
}

func tokensToDTO(t core.Tokens) TokensDTO {
	return TokensDTO{
		Input:           t.InputTokens,
		CachedInput:     t.CachedInputTokens,
		CacheWriteInput: t.CacheWriteInputTokens,
		Output:          t.OutputTokens,
		Reasoning:       t.ReasoningTokens,
	}
}
