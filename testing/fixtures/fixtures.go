// Package fixtures holds deterministic test data: captured runner output and
// domain value builders.
package fixtures

import (
	"embed"
	"time"

	"github.com/viktordanov/uagent/domain"
)

//go:embed runner/*.jsonl
var runnerFiles embed.FS

const (
	SessionID = "11111111-1111-4111-8111-111111111111"
	RunID     = "20260923-120000-11111111"
	Workspace = "/workspace"
)

// Tool is the tool name used by built events.
const Tool = "Bash"

// T0 is the reference time for built events.
var T0 = time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)

// At returns T0 plus d.
func At(d time.Duration) time.Time { return T0.Add(d) }

// RunnerOutput returns a captured unreal-agent-runner stdout, with local paths
// replaced by /workspace and /state:
//   - simple.jsonl: two parallel Bash calls, then a final answer ("hello")
//   - parallel.jsonl: three sequential Bash calls over four turns, final answer "A; B"
//   - timeout.jsonl: one long Bash call, killed by a timeout (no final answer)
func RunnerOutput(name string) []byte {
	data, err := runnerFiles.ReadFile("runner/" + name)
	if err != nil {
		panic(err)
	}

	return data
}

// Request returns a valid run request.
func Request() domain.RunRequest {
	return RequestWith(func(*domain.RunRequest) {})
}

// RequestWith returns Request modified by fn.
func RequestWith(fn func(*domain.RunRequest)) domain.RunRequest {
	req := domain.RunRequest{
		RunID:     RunID,
		SessionID: SessionID,
		Prompt:    "Summarize this project.",
		Provider:  "openai-codex",
		Model:     "gpt-6-sol",
		Effort:    "high",
		Workspace: Workspace,
	}
	fn(&req)

	return req
}

// ToolRun returns the events of one tool call that starts at start and runs for d.
func ToolRun(callID string, start, d time.Duration, ok bool) []domain.Event {
	return []domain.Event{
		domain.ToolCalled{At: At(start), CallID: callID, Name: Tool, Label: "echo " + callID},
		domain.ToolStarted{At: At(start), CallID: callID, OpID: "op-" + callID, Name: Tool},
		domain.ToolFinished{At: At(start + d), CallID: callID, OpID: "op-" + callID, Name: Tool, OK: ok, Duration: d},
	}
}

// Turn returns TurnStarted at start and ModelResponded after d.
func Turn(n int, start, d time.Duration, usage domain.Tokens) []domain.Event {
	return []domain.Event{
		domain.TurnStarted{At: At(start), Turn: n},
		domain.ModelResponded{At: At(start + d), Turn: n, Duration: d, Usage: usage},
	}
}
