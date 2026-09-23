package harness

import (
	"encoding/json"
	"time"

	"github.com/viktordanov/uagent/core"
)

// Wire types for unreal-agent-runner v0.1.x stdout, stdin, and session files. Each stdout line is either a
// session item (Sequence/RecordedAt/Kind/Data) or {"type":"error"}.

type requestDTO struct {
	Prompt          string   `json:"prompt"`
	ThinkingLevel   string   `json:"thinking_level,omitempty"`
	Model           string   `json:"model,omitempty"`
	SessionID       string   `json:"session_id,omitempty"`
	SystemPrompt    string   `json:"system_prompt,omitempty"`
	DisallowedTools []string `json:"disallowed_tools,omitempty"`
	MaxAttempts     int      `json:"max_attempts,omitempty"`
}

func requestToDTO(req core.Request) requestDTO {
	return requestDTO{
		Prompt:          req.Prompt,
		ThinkingLevel:   req.Effort,
		Model:           req.Model,
		SessionID:       req.SessionID,
		SystemPrompt:    req.SystemPrompt,
		DisallowedTools: req.DisallowedTools,
		MaxAttempts:     req.MaxAttempts,
	}
}

type itemDTO struct {
	Sequence   int64           `json:"Sequence"`
	RecordedAt time.Time       `json:"RecordedAt"`
	Kind       string          `json:"Kind"`
	Data       json.RawMessage `json:"Data"`
	Type       string          `json:"type"`
	Message    string          `json:"message"`
}

type modelResponseDTO struct {
	TurnID   string `json:"TurnID"`
	Response struct {
		Stop    string          `json:"Stop"`
		Output  []outputItemDTO `json:"Output"`
		Usage   usageDTO        `json:"Usage"`
		Failure *struct {
			Code    string `json:"Code"`
			Message string `json:"Message"`
		} `json:"Failure"`
	} `json:"Response"`
}

type outputItemDTO struct {
	Type string          `json:"Type"`
	Data json.RawMessage `json:"Data"`
}

type messageDTO struct {
	Role  string `json:"Role"`
	Text  string `json:"Text"`
	Phase string `json:"Phase"`
}

type toolCallDTO struct {
	CallID    string `json:"CallID"`
	Name      string `json:"Name"`
	Arguments string `json:"Arguments"`
}

type reasoningDTO struct {
	Summary []string `json:"Summary"`
}

type usageDTO struct {
	InputTokens           int64 `json:"InputTokens"`
	CachedInputTokens     int64 `json:"CachedInputTokens"`
	CacheWriteInputTokens int64 `json:"CacheWriteInputTokens"`
	OutputTokens          int64 `json:"OutputTokens"`
	ReasoningTokens       int64 `json:"ReasoningTokens"`
}

func (u usageDTO) toCore() core.Tokens {
	return core.Tokens{
		InputTokens:           u.InputTokens,
		CachedInputTokens:     u.CachedInputTokens,
		CacheWriteInputTokens: u.CacheWriteInputTokens,
		OutputTokens:          u.OutputTokens,
		ReasoningTokens:       u.ReasoningTokens,
	}
}

type toolCallStatusDTO struct {
	CallID string `json:"CallID"`
	Status struct {
		Error string `json:"Error"`
	} `json:"Status"`
	Operations []operationDTO `json:"Operations"`
}

type operationDTO struct {
	ID     string          `json:"ID"`
	Type   string          `json:"Type"`
	Status string          `json:"Status"`
	State  json.RawMessage `json:"State"`
}

type shellStateDTO struct {
	ProcessGroupID int `json:"ProcessGroupID"`
	Result         *struct {
		ExitCode int `json:"ExitCode"`
	} `json:"Result"`
	TerminalError string `json:"TerminalError"`
}

// sessionRecordDTO is one line of the runner's session file.
type sessionRecordDTO struct {
	Type string `json:"type"`
	Data struct {
		Operation struct {
			ID     string `json:"ID"`
			Status string `json:"Status"`
			State  struct {
				ProcessGroupID int `json:"ProcessGroupID"`
			} `json:"State"`
		} `json:"Operation"`
	} `json:"data"`
}

func isTerminal(status string) bool {
	return status == "completed" || status == "failed" || status == "canceled"
}
