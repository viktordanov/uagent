package harness

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/viktordanov/uagent/core"
)

type callInfo struct{ name, label string }

type opInfo struct {
	start time.Time
	done  bool
}

// Decoder turns runner stdout lines into core events. It keeps
// state across lines (turn numbers, tool names, operation lifecycles).
type Decoder struct {
	turns     int
	turnStart time.Time
	calls     map[string]callInfo
	ops       map[string]*opInfo
	failed    map[string]bool
}

func NewDecoder() *Decoder {
	return &Decoder{
		calls:  map[string]callInfo{},
		ops:    map[string]*opInfo{},
		failed: map[string]bool{},
	}
}

// Decode converts one stdout line. Unparseable lines become RunnerError events.
func (d *Decoder) Decode(line []byte) []core.Event {
	if strings.TrimSpace(string(line)) == "" {
		return nil
	}
	var item itemDTO
	if err := json.Unmarshal(line, &item); err != nil {
		return []core.Event{core.RunnerError{At: time.Now(), Message: "unparseable runner output: " + oneLine(string(line), 200)}}
	}
	if item.Type == "error" {
		return []core.Event{core.RunnerError{At: time.Now(), Message: item.Message}}
	}
	switch item.Kind {
	case "turn":
		d.turns++
		d.turnStart = item.RecordedAt

		return []core.Event{core.TurnStarted{At: item.RecordedAt, Turn: d.turns}}
	case "model_response":
		return d.decodeResponse(item)
	case "tool_call_status":
		return d.decodeToolStatus(item)
	}

	return nil
}

func (d *Decoder) decodeResponse(item itemDTO) []core.Event {
	var dto modelResponseDTO
	if err := json.Unmarshal(item.Data, &dto); err != nil {
		return []core.Event{core.RunnerError{At: item.RecordedAt, Message: fmt.Sprintf("failed to decode model_response: %v", err)}}
	}
	resp := dto.Response
	responded := core.ModelResponded{At: item.RecordedAt, Turn: d.turns, Usage: resp.Usage.toCore(), Stop: resp.Stop}
	if !d.turnStart.IsZero() {
		responded.Duration = item.RecordedAt.Sub(d.turnStart)
	}
	if resp.Failure != nil {
		responded.Failure = strings.TrimSpace(resp.Failure.Code + ": " + resp.Failure.Message)
	}
	events := []core.Event{responded}
	for _, out := range resp.Output {
		switch out.Type {
		case "tool_call":
			var c toolCallDTO
			if json.Unmarshal(out.Data, &c) != nil {
				continue
			}
			info := callInfo{name: c.Name, label: describeCall(c)}
			d.calls[c.CallID] = info
			events = append(events, core.ToolCalled{At: item.RecordedAt, CallID: c.CallID, Name: info.name, Label: info.label})
		case "message":
			var m messageDTO
			if json.Unmarshal(out.Data, &m) != nil || m.Role != "assistant" {
				continue
			}
			events = append(events, core.AssistantMessage{At: item.RecordedAt, Text: m.Text, Final: m.Phase == "final_answer"})
		case "reasoning":
			var r reasoningDTO
			if json.Unmarshal(out.Data, &r) != nil {
				continue
			}
			for _, s := range r.Summary {
				events = append(events, core.ReasoningSummary{At: item.RecordedAt, Text: s})
			}
		}
	}

	return events
}

func (d *Decoder) decodeToolStatus(item itemDTO) []core.Event {
	var dto toolCallStatusDTO
	if err := json.Unmarshal(item.Data, &dto); err != nil {
		return []core.Event{core.RunnerError{At: item.RecordedAt, Message: fmt.Sprintf("failed to decode tool_call_status: %v", err)}}
	}
	call := d.calls[dto.CallID]
	var events []core.Event
	if dto.Status.Error != "" && !d.failed[dto.CallID] {
		d.failed[dto.CallID] = true
		events = append(events, core.ToolFinished{
			At: item.RecordedAt, CallID: dto.CallID, Name: call.name, Label: call.label,
			Detail: oneLine(dto.Status.Error, 160),
		})
	}
	for _, op := range dto.Operations {
		info, seen := d.ops[op.ID]
		if !seen {
			info = &opInfo{start: item.RecordedAt}
			d.ops[op.ID] = info
			events = append(events, core.ToolStarted{At: item.RecordedAt, CallID: dto.CallID, OpID: op.ID, Name: call.name, Label: call.label})
		}
		if info.done || !isTerminal(op.Status) {
			continue
		}
		info.done = true
		ok, detail := operationOutcome(op)
		events = append(events, core.ToolFinished{
			At: item.RecordedAt, CallID: dto.CallID, OpID: op.ID, Name: call.name, Label: call.label,
			OK: ok, Detail: detail, Duration: item.RecordedAt.Sub(info.start),
		})
	}

	return events
}

func operationOutcome(op operationDTO) (ok bool, detail string) {
	ok, detail = op.Status == "completed", op.Status
	if op.Type != "shell" {
		return ok, detail
	}
	var s shellStateDTO
	if json.Unmarshal(op.State, &s) != nil {
		return ok, detail
	}
	if s.Result != nil {
		detail = fmt.Sprintf("exit %d", s.Result.ExitCode)
		ok = ok && s.Result.ExitCode == 0
	}
	if s.TerminalError != "" {
		ok, detail = false, oneLine(s.TerminalError, 120)
	}

	return ok, detail
}

func describeCall(c toolCallDTO) string {
	if c.Name == "Bash" {
		var args struct {
			Command string `json:"command"`
		}
		if json.Unmarshal([]byte(c.Arguments), &args) == nil && args.Command != "" {
			return oneLine(args.Command, 120)
		}
	}

	return oneLine(c.Arguments, 120)
}

// ReadEvents decodes saved runner output (events.jsonl) and sends every event to sink.
func ReadEvents(r io.Reader, sink core.Sink) error {
	decoder := NewDecoder()
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		for _, e := range decoder.Decode(line) {
			sink(e)
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("failed to read events: %w", err)
		}
	}
}

// oneLine collapses whitespace and truncates to limit runes.
func oneLine(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > limit {
		return string(r[:limit-1]) + "…"
	}

	return s
}
