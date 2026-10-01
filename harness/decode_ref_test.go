package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/viktordanov/uagent/core"
	"github.com/viktordanov/uagent/testing/fixtures"
)

// TestDecoder_MatchesReference decodes the same lines with Decoder and with
// refDecoder, the decoder before the single-pass decode (kept below as it was),
// and requires the same events.
func TestDecoder_MatchesReference(t *testing.T) {
	inputs := map[string][]byte{"large": fixtures.LargeRunnerOutput(40), "odd lines": []byte(strings.Join(oddLines, "\n"))}
	for _, name := range []string{"simple.jsonl", "parallel.jsonl", "timeout.jsonl", "error.jsonl"} {
		inputs[name] = fixtures.RunnerOutput(name)
	}
	for name, data := range inputs {
		t.Run(name, func(t *testing.T) {
			start := time.Now()
			got, want := NewDecoder(), newRefDecoder()
			lines := bytes.SplitAfter(data, []byte("\n"))
			require.Greater(t, len(lines), 1)
			for i, line := range lines {
				assert.Equal(t, normalize(want.Decode(line), start), normalize(got.Decode(line), start), "line %d: %s", i+1, line)
			}
		})
	}
}

// BenchmarkDecode compares Decoder with refDecoder on LargeRunnerOutput.
func BenchmarkDecode(b *testing.B) {
	data := fixtures.LargeRunnerOutput(100)
	lines := bytes.SplitAfter(data, []byte("\n"))
	b.Run("decoder", func(b *testing.B) {
		b.SetBytes(int64(len(data)))
		b.ReportAllocs()
		for b.Loop() {
			d := NewDecoder()
			for _, line := range lines {
				d.Decode(line)
			}
		}
	})
	b.Run("reference", func(b *testing.B) {
		b.SetBytes(int64(len(data)))
		b.ReportAllocs()
		for b.Loop() {
			d := newRefDecoder()
			for _, line := range lines {
				d.Decode(line)
			}
		}
	})
}

// oddLines reach every lenient and error path of the decode kind by kind.
var oddLines = []string{
	``, "   ", `not json`, `{"type":"error","message":"no model"}`, `{"Sequence":"1"}`,
	`{"RecordedAt":"2026-09-23T12:00:00Z","Kind":"turn","Data":5}`,
	`{"RecordedAt":"2026-09-23T12:00:01Z","Kind":"turn","Data":{"ID":7}}`,
	`{"RecordedAt":"2026-09-23T12:00:02Z","Kind":"turn"}`,
	`{"RecordedAt":"2026-09-23T12:00:03Z","Kind":"turn","Data":{"ID":"t1","Response":5}}`,
	`{"RecordedAt":"2026-09-23T12:00:04Z","Kind":"input","Data":"x"}`,
	`{"RecordedAt":"2026-09-23T12:00:05Z","Kind":"input","Data":{"ID":"i1","Kind":"external","Payload":{"text":"hi"}}}`,
	`{"RecordedAt":"2026-09-23T12:00:06Z","Kind":"input","Data":{"ID":"i2","Kind":"external"}}`,
	`{"RecordedAt":"2026-09-23T12:00:07Z","Kind":"input","Data":{"ID":"i3","Kind":"control","Payload":"settings"}}`,
	`{"RecordedAt":"2026-09-23T12:00:08Z","Kind":"input","Data":{"ID":"i4","Kind":"other","Payload":1}}`,
	`{"RecordedAt":"2026-09-23T12:00:09Z","Kind":"input","Data":{"ID":"i5","Kind":"external","Payload":"hi","Operations":3}}`,
	`{"RecordedAt":"2026-09-23T12:00:10Z","Kind":"model_response","Data":[]}`,
	`{"RecordedAt":"2026-09-23T12:00:11Z","Kind":"model_response","Data":{"TurnID":"t1","Response":{"Stop":5}}}`,
	`{"RecordedAt":"2026-09-23T12:00:12Z","Kind":"model_response","Data":{"TurnID":"t1","Response":{"Stop":"complete","Failure":{"Code":"rate_limit","Message":"slow down"},"Output":[` +
		`{"Type":"tool_call","Data":"x"},{"Type":"tool_call","Data":{"CallID":"c1","Name":"Bash","Arguments":"{\"command\":\"ls  -la\"}","Summary":5}},` +
		`{"Type":"message","Data":{"Role":"user","Text":"hi"}},{"Type":"message","Data":{"Role":"assistant","Text":5}},{"Type":"message","Data":{"Role":"assistant","Text":"ok","Phase":"final_answer","CallID":[]}},` +
		`{"Type":"reasoning","Data":{"Summary":"x"}},{"Type":"reasoning","Data":{"Summary":["a","b"],"Name":1}},{"Type":"web_search","Data":{"Summary":7}}]}}}`,
	`{"RecordedAt":"2026-09-23T12:00:13Z","Kind":"model_response","Data":{"TurnID":"t1","Response":{"Stop":"complete","Output":[{"Type":"tool_call","Data":{"CallID":"c2","Name":"Read","Arguments":"{\"path\":\"a.go\"}"}}]}}}`,
	`{"RecordedAt":"2026-09-23T12:00:14Z","Kind":"tool_call_status","Data":5}`,
	`{"RecordedAt":"2026-09-23T12:00:15Z","Kind":"tool_call_status","Data":{"CallID":"c1","Status":{"Error":"tool   failed\nbadly"}}}`,
	`{"RecordedAt":"2026-09-23T12:00:16Z","Kind":"tool_call_status","Data":{"CallID":"c1","Status":{"Error":"again"}}}`,
	`{"RecordedAt":"2026-09-23T12:00:17Z","Kind":"tool_call_status","Data":{"CallID":"c1","Operations":[{"ID":"o1","Type":"shell","Status":"running","State":"x"}]}}`,
	`{"RecordedAt":"2026-09-23T12:00:18Z","Kind":"tool_call_status","Data":{"CallID":"c1","Operations":[{"ID":"o1","Type":"shell","Status":"completed","State":{"Result":5,"OutPath":"/o","TerminalError":"killed"}}]}}`,
	`{"RecordedAt":"2026-09-23T12:00:19Z","Kind":"tool_call_status","Data":{"CallID":"c2","Operations":[{"ID":"o2","Type":"read","Status":"failed","State":{"Result":"x"}}]}}`,
	`{"RecordedAt":"2026-09-23T12:00:20Z","Kind":"tool_call_status","Data":{"CallID":"c2","Operations":[{"ID":3}]}}`,
	`{"RecordedAt":"2026-09-23T12:00:21Z","Kind":"tool_call_status","Data":{"CallID":"c3","Operations":[{"ID":"o3","Type":"shell","Status":"canceled","State":null}]}}`,
	`{"RecordedAt":"2026-09-23T12:00:22Z","Kind":"tool_call_status","Data":{"CallID":"c3","Operations":[{"ID":"o4","Type":"shell","Status":"completed","State":{"Result":{"ExitCode":2},"OutPath":"/o4","ErrPath":"/e4"}}]}}`,
	`{"RecordedAt":"2026-09-23T12:00:23Z","Kind":"tool_call_status","Data":{"CallID":"c3","Operations":[{"ID":"o5","Type":"shell","Status":"failed","State":{"TerminalError":"  no   such\tshell "}}]}}`,
	`{"RecordedAt":"2026-09-23T12:00:24Z","Kind":"tool_call_status","Data":{"CallID":"c3","Operations":[{"ID":"o5","Type":"shell","Status":"completed"}]}}`,
	`{"RecordedAt":"2026-09-23T12:00:25Z","Kind":"something_new","Data":{"Operations":"x"}}`,
	`{"RecordedAt":"2026-09-23T12:00:25Z","Kind":"something_new","Data":{"Operations":[]}}`,
	`{"type":"error","message":"boom","Kind":"turn","Data":{"ID":5}}`,
	`{"RecordedAt":"2026-09-23T12:00:25Z","Kind":"tool_call_status","Data":{"CallID":"c3","Operations":[{"ID":"o6","Type":"shell","Status":"failed","State":{"TerminalError":"` + strings.Repeat("é ", 200) + `"}}]}}`,
	`{"RecordedAt":"2026-09-23T12:00:26Z","Kind":"model_response","Data":{"TurnID":"unknown","Response":{"Stop":"complete"}}}`,
}

// normalize clears the time of a RunnerError stamped with time.Now, which
// differs between the two decodes, and the reference's type names in messages.
func normalize(events []core.Event, start time.Time) []core.Event {
	for i, e := range events {
		if r, ok := e.(core.RunnerError); ok {
			if !r.At.Before(start) {
				r.At = time.Time{}
			}
			r.Message = strings.ReplaceAll(r.Message, "DTORef", "DTO")
			events[i] = r
		}
	}

	return events
}

// refDecoder turns runner stdout lines into core events. It keeps
// state across lines (turn numbers, tool names, operation lifecycles).
type refDecoder struct {
	turns     int
	turnStart time.Time
	turnIDs   map[string]int
	calls     map[string]callInfo
	ops       map[string]*opInfo
	failed    map[string]bool
}

func newRefDecoder() *refDecoder {
	return &refDecoder{
		turnIDs: map[string]int{},
		calls:   map[string]callInfo{},
		ops:     map[string]*opInfo{},
		failed:  map[string]bool{},
	}
}

// Decode converts one stdout line. Unparseable lines become RunnerError events.
func (d *refDecoder) Decode(line []byte) []core.Event {
	if strings.TrimSpace(string(line)) == "" {
		return nil
	}
	var item itemDTORef
	if err := json.Unmarshal(line, &item); err != nil {
		return []core.Event{core.RunnerError{At: time.Now(), Message: "unparseable runner output: " + oneLine(string(line), 200)}}
	}
	if item.Type == "error" {
		return []core.Event{core.RunnerError{At: time.Now(), Message: item.Message}}
	}
	switch item.Kind {
	case "input":
		return refDecodeInput(item)
	case "turn":
		var turn turnDTO
		_ = json.Unmarshal(item.Data, &turn) // a missing ID leaves TurnID empty
		d.turns++
		d.turnStart = item.RecordedAt
		if turn.ID != "" {
			d.turnIDs[turn.ID] = d.turns
		}

		return []core.Event{core.TurnStarted{At: item.RecordedAt, Turn: d.turns, TurnID: turn.ID}}
	case "model_response":
		return d.decodeResponse(item)
	case "tool_call_status":
		return d.decodeToolStatus(item)
	}

	return nil
}

func (d *refDecoder) decodeResponse(item itemDTORef) []core.Event {
	var dto modelResponseDTORef
	if err := json.Unmarshal(item.Data, &dto); err != nil {
		return []core.Event{core.RunnerError{At: item.RecordedAt, Message: fmt.Sprintf("failed to decode model_response: %v", err)}}
	}
	resp := dto.Response
	turn := d.turns
	if n, ok := d.turnIDs[dto.TurnID]; ok {
		turn = n
	}
	responded := core.ModelResponded{At: item.RecordedAt, Turn: turn, TurnID: dto.TurnID, Usage: resp.Usage.toCore(), Stop: resp.Stop}
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
			info := callInfo{name: c.Name, label: refDescribeCall(c)}
			d.calls[c.CallID] = info
			events = append(events, core.ToolCalled{
				At: item.RecordedAt, CallID: c.CallID, Name: info.name, Label: info.label, Arguments: c.Arguments,
			})
		case "message":
			var m messageDTO
			if json.Unmarshal(out.Data, &m) != nil || m.Role != "assistant" {
				continue
			}
			events = append(events, core.AssistantMessage{At: item.RecordedAt, Turn: turn, Text: m.Text, Final: m.Phase == "final_answer"})
		case "reasoning":
			var r reasoningDTO
			if json.Unmarshal(out.Data, &r) != nil {
				continue
			}
			for _, s := range r.Summary {
				events = append(events, core.ReasoningSummary{At: item.RecordedAt, Turn: turn, Text: s})
			}
		}
	}

	return events
}

func (d *refDecoder) decodeToolStatus(item itemDTORef) []core.Event {
	var dto toolCallStatusDTORef
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
		outPath, errPath := refShellPaths(op)
		info, seen := d.ops[op.ID]
		if !seen {
			info = &opInfo{start: item.RecordedAt}
			d.ops[op.ID] = info
			events = append(events, core.ToolStarted{
				At: item.RecordedAt, CallID: dto.CallID, OpID: op.ID, Name: call.name, Label: call.label,
				OpType: op.Type, OutPath: outPath, ErrPath: errPath,
			})
		}
		if info.done || !isTerminal(op.Status) {
			continue
		}
		info.done = true
		ok, detail := refOperationOutcome(op)
		events = append(events, core.ToolFinished{
			At: item.RecordedAt, CallID: dto.CallID, OpID: op.ID, Name: call.name, Label: call.label,
			OK: ok, Detail: detail, Duration: item.RecordedAt.Sub(info.start),
			OpType: op.Type, OutPath: outPath, ErrPath: errPath,
		})
	}

	return events
}

// refDecodeInput turns a persisted inbox input into a UserMessage or ControlInput.
func refDecodeInput(item itemDTORef) []core.Event {
	var in inputDTO
	if err := json.Unmarshal(item.Data, &in); err != nil {
		return []core.Event{core.RunnerError{At: item.RecordedAt, Message: fmt.Sprintf("failed to decode input: %v", err)}}
	}
	switch in.Kind {
	case "external":
		var text string
		if err := json.Unmarshal(in.Payload, &text); err != nil {
			text = string(in.Payload)
		}

		return []core.Event{core.UserMessage{At: item.RecordedAt, ID: in.ID, Text: text}}
	case "control":
		var c controlDTO
		if err := json.Unmarshal(in.Payload, &c); err != nil {
			return []core.Event{core.RunnerError{At: item.RecordedAt, Message: fmt.Sprintf("failed to decode control input: %v", err)}}
		}

		return []core.Event{core.ControlInput{
			At: item.RecordedAt, ID: in.ID, Mode: c.Mode, Effort: c.Parameters.ReasoningEffort, Reason: c.Reason,
		}}
	}

	return nil
}

// refShellPaths returns a shell operation's output files. The runner reports
// them once the operation has output; they are empty before that.
func refShellPaths(op operationDTORef) (outPath, errPath string) {
	if op.Type != "shell" {
		return "", ""
	}
	var s shellStateDTO
	if json.Unmarshal(op.State, &s) != nil {
		return "", ""
	}

	return s.OutPath, s.ErrPath
}

func refOperationOutcome(op operationDTORef) (ok bool, detail string) {
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

func refDescribeCall(c toolCallDTO) string {
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

type itemDTORef struct {
	Sequence   int64           `json:"Sequence"`
	RecordedAt time.Time       `json:"RecordedAt"`
	Kind       string          `json:"Kind"`
	Data       json.RawMessage `json:"Data"`
	Type       string          `json:"type"`
	Message    string          `json:"message"`
}

type modelResponseDTORef struct {
	TurnID   string `json:"TurnID"`
	Response struct {
		Stop    string             `json:"Stop"`
		Output  []outputItemDTORef `json:"Output"`
		Usage   usageDTO           `json:"Usage"`
		Failure *struct {
			Code    string `json:"Code"`
			Message string `json:"Message"`
		} `json:"Failure"`
	} `json:"Response"`
}

type outputItemDTORef struct {
	Type string          `json:"Type"`
	Data json.RawMessage `json:"Data"`
}

type toolCallStatusDTORef struct {
	TurnID string `json:"TurnID"`
	CallID string `json:"CallID"`
	Status struct {
		Error string `json:"Error"`
	} `json:"Status"`
	Operations []operationDTORef `json:"Operations"`
}

type operationDTORef struct {
	ID     string          `json:"ID"`
	Type   string          `json:"Type"`
	Status string          `json:"Status"`
	State  json.RawMessage `json:"State"`
}
