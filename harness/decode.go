package harness

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/viktordanov/uagent/core"
)

// shellOp is the operation type of a Bash call.
const shellOp = "shell"

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
	turnIDs   map[string]int
	calls     map[string]callInfo
	ops       map[string]*opInfo
	failed    map[string]bool
}

func NewDecoder() *Decoder {
	return &Decoder{
		turnIDs: map[string]int{},
		calls:   map[string]callInfo{},
		ops:     map[string]*opInfo{},
		failed:  map[string]bool{},
	}
}

// Decode converts one stdout line. Unparseable lines become RunnerError events.
//
// A line decodes in one pass into dataDTO, the union of the Data shapes. A line
// that does not fit it (malformed, or a field of an unexpected type) is decoded
// again kind by kind, which keeps the lenient handling and the error messages
// of a bad Data.
func (d *Decoder) Decode(line []byte) []core.Event {
	if len(bytes.TrimSpace(line)) == 0 {
		return nil
	}
	var item lineDTO
	if json.Unmarshal(line, &item) != nil {
		return d.decodeByKind(line)
	}
	if item.Type == "error" {
		return []core.Event{core.RunnerError{At: time.Now(), Message: item.Message}}
	}
	at, data := item.RecordedAt, item.Data
	switch item.Kind {
	case "input":
		return inputEvents(at, data.inputDTO)
	case "turn":
		return d.turnStarted(at, data.ID)
	case "model_response":
		return d.responseEvents(at, data.TurnID, data.Response)
	case "tool_call_status":
		return d.statusEvents(at, data.CallID, data.Status, data.Operations)
	}

	return nil
}

// decodeByKind decodes a line that does not fit dataDTO: Data by its kind, then
// each output by its type and each shell operation's state.
func (d *Decoder) decodeByKind(line []byte) []core.Event {
	var item itemDTO
	if err := json.Unmarshal(line, &item); err != nil {
		return []core.Event{core.RunnerError{At: time.Now(), Message: "unparseable runner output: " + oneLine(string(line), 200)}}
	}
	if item.Type == "error" {
		return []core.Event{core.RunnerError{At: time.Now(), Message: item.Message}}
	}
	at := item.RecordedAt
	switch item.Kind {
	case "input":
		var in inputDTO
		if err := json.Unmarshal(item.Data, &in); err != nil {
			return []core.Event{core.RunnerError{At: at, Message: fmt.Sprintf("failed to decode input: %v", err)}}
		}

		return inputEvents(at, in)
	case "turn":
		var turn turnDTO
		_ = json.Unmarshal(item.Data, &turn) // a missing ID leaves TurnID empty

		return d.turnStarted(at, turn.ID)
	case "model_response":
		var dto modelResponseDTO
		if err := json.Unmarshal(item.Data, &dto); err != nil {
			return []core.Event{core.RunnerError{At: at, Message: fmt.Sprintf("failed to decode model_response: %v", err)}}
		}

		return d.responseEvents(at, dto.TurnID, decodeOutputs(dto))
	case "tool_call_status":
		var dto toolCallStatusDTO
		if err := json.Unmarshal(item.Data, &dto); err != nil {
			return []core.Event{core.RunnerError{At: at, Message: fmt.Sprintf("failed to decode tool_call_status: %v", err)}}
		}

		return d.statusEvents(at, dto.CallID, dto.Status, decodeStates(dto.Operations))
	}

	return nil
}

// decodeOutputs decodes each output by its type and drops one that does not decode.
func decodeOutputs(dto modelResponseDTO) responseDTO {
	raw := dto.Response
	resp := responseDTO{Stop: raw.Stop, Usage: raw.Usage, Failure: raw.Failure}
	for _, out := range raw.Output {
		typed := outputDataDTO{Type: out.Type}
		var err error
		switch out.Type {
		case "tool_call":
			err = json.Unmarshal(out.Data, &typed.Data.toolCallDTO)
		case "message":
			err = json.Unmarshal(out.Data, &typed.Data.messageDTO)
		case "reasoning":
			err = json.Unmarshal(out.Data, &typed.Data.reasoningDTO)
		}
		if err == nil {
			resp.Output = append(resp.Output, typed)
		}
	}

	return resp
}

// decodeStates decodes the state of each shell operation. A state that does not
// decode reads as an empty one.
func decodeStates(raw []operationDTO) []shellOperationDTO {
	ops := make([]shellOperationDTO, 0, len(raw))
	for _, op := range raw {
		typed := shellOperationDTO{ID: op.ID, Type: op.Type, Status: op.Status}
		if op.Type == shellOp && json.Unmarshal(op.State, &typed.State) != nil {
			typed.State = shellStateDTO{}
		}
		ops = append(ops, typed)
	}

	return ops
}

func (d *Decoder) turnStarted(at time.Time, turnID string) []core.Event {
	d.turns++
	d.turnStart = at
	if turnID != "" {
		d.turnIDs[turnID] = d.turns
	}

	return []core.Event{core.TurnStarted{At: at, Turn: d.turns, TurnID: turnID}}
}

func (d *Decoder) responseEvents(at time.Time, turnID string, resp responseDTO) []core.Event {
	turn := d.turns
	if n, ok := d.turnIDs[turnID]; ok {
		turn = n
	}
	responded := core.ModelResponded{At: at, Turn: turn, TurnID: turnID, Usage: resp.Usage.toCore(), Stop: resp.Stop}
	if !d.turnStart.IsZero() {
		responded.Duration = at.Sub(d.turnStart)
	}
	if resp.Failure != nil {
		responded.Failure = strings.TrimSpace(resp.Failure.Code + ": " + resp.Failure.Message)
	}
	events := []core.Event{responded}
	for _, out := range resp.Output {
		switch out.Type {
		case "tool_call":
			c := out.Data.toolCallDTO
			info := callInfo{name: c.Name, label: describeCall(c)}
			d.calls[c.CallID] = info
			events = append(events, core.ToolCalled{
				At: at, CallID: c.CallID, Name: info.name, Label: info.label, Arguments: c.Arguments,
			})
		case "message":
			m := out.Data.messageDTO
			if m.Role != "assistant" {
				continue
			}
			events = append(events, core.AssistantMessage{At: at, Turn: turn, Text: m.Text, Final: m.Phase == "final_answer"})
		case "reasoning":
			for _, s := range out.Data.Summary {
				events = append(events, core.ReasoningSummary{At: at, Turn: turn, Text: s})
			}
		}
	}

	return events
}

func (d *Decoder) statusEvents(at time.Time, callID string, status callStatusDTO, ops []shellOperationDTO) []core.Event {
	call := d.calls[callID]
	var events []core.Event
	if status.Error != "" && !d.failed[callID] {
		d.failed[callID] = true
		events = append(events, core.ToolFinished{
			At: at, CallID: callID, Name: call.name, Label: call.label,
			Detail: oneLine(status.Error, 160),
		})
	}
	for _, op := range ops {
		outPath, errPath := shellPaths(op)
		info, seen := d.ops[op.ID]
		if !seen {
			info = &opInfo{start: at}
			d.ops[op.ID] = info
			events = append(events, core.ToolStarted{
				At: at, CallID: callID, OpID: op.ID, Name: call.name, Label: call.label,
				OpType: op.Type, OutPath: outPath, ErrPath: errPath,
			})
		}
		if info.done || !isTerminal(op.Status) {
			continue
		}
		info.done = true
		ok, detail := operationOutcome(op)
		events = append(events, core.ToolFinished{
			At: at, CallID: callID, OpID: op.ID, Name: call.name, Label: call.label,
			OK: ok, Detail: detail, Duration: at.Sub(info.start),
			OpType: op.Type, OutPath: outPath, ErrPath: errPath,
		})
	}

	return events
}

// inputEvents turns a persisted inbox input into a UserMessage,
// DeveloperMessage, or ControlInput.
func inputEvents(at time.Time, in inputDTO) []core.Event {
	switch in.Kind {
	case "external":
		return []core.Event{core.UserMessage{At: at, ID: in.ID, Text: inputText(in.Payload)}}
	case "developer":
		return []core.Event{core.DeveloperMessage{At: at, ID: in.ID, Text: inputText(in.Payload)}}
	case "control":
		var c controlDTO
		if err := json.Unmarshal(in.Payload, &c); err != nil {
			return []core.Event{core.RunnerError{At: at, Message: fmt.Sprintf("failed to decode control input: %v", err)}}
		}

		return []core.Event{core.ControlInput{
			At: at, ID: in.ID, Mode: c.Mode, Effort: c.Parameters.ReasoningEffort, Reason: c.Reason,
		}}
	}

	return nil
}

// inputText is a message input's text: its JSON string payload, else the
// payload as is.
func inputText(payload json.RawMessage) string {
	var text string
	if err := json.Unmarshal(payload, &text); err != nil {
		return string(payload)
	}

	return text
}

// shellPaths returns a shell operation's output files. The runner reports
// them once the operation has output; they are empty before that.
func shellPaths(op shellOperationDTO) (outPath, errPath string) {
	if op.Type != shellOp {
		return "", ""
	}

	return op.State.OutPath, op.State.ErrPath
}

func operationOutcome(op shellOperationDTO) (ok bool, detail string) {
	ok, detail = op.Status == "completed", op.Status
	if op.Type != shellOp {
		return ok, detail
	}
	s := op.State
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
	br := bufio.NewReaderSize(r, 64<<10) // longer lines are joined below
	var long []byte
	for {
		// The line is valid until the next read: Decode copies what it keeps.
		line, err := br.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			long = append(long[:0], line...)
			for err == bufio.ErrBufferFull {
				line, err = br.ReadSlice('\n')
				long = append(long, line...)
			}
			line = long
		}
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
