package main

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
)

// Wire types for the runner's stdout. Each line is either a session item
// (Sequence/RecordedAt/Kind/Data) or a runner error event ({"type":"error"}).

type rawEvent struct {
	Sequence   int64           `json:"Sequence"`
	RecordedAt time.Time       `json:"RecordedAt"`
	Kind       string          `json:"Kind"`
	Data       json.RawMessage `json:"Data"`
	Type       string          `json:"type"`
	Message    string          `json:"message"`
}

type modelResponse struct {
	TurnID   string
	Response struct {
		ID      string
		Stop    string
		Output  []outputItem
		Usage   usageData
		Failure *struct{ Code, Message string }
	}
}

type outputItem struct {
	Type string
	Data json.RawMessage
}

type messageData struct{ Role, Text, Phase string }

type toolCallData struct{ CallID, Name, Arguments string }

type reasoningData struct{ Summary []string }

type toolCallStatus struct {
	TurnID, CallID string
	Status         struct {
		Error      string
		WaitingFor []string
	}
	Operations []operationData
}

type operationData struct {
	ID, Type, Status string
	State            json.RawMessage
}

type shellState struct {
	Input          struct{ Command string }
	ProcessGroupID int
	Result         *struct{ ExitCode int }
	TerminalError  string
}

// usageData is the runner's usage block. InputTokens includes cached and
// cache-write tokens; OutputTokens includes reasoning tokens.
type usageData struct {
	InputTokens, CachedInputTokens, CacheWriteInputTokens, OutputTokens, ReasoningTokens int64
}

// Tokens is the summed usage as written to summary JSON.
type Tokens struct {
	InputTokens           int64 `json:"input"`
	CachedInputTokens     int64 `json:"cached_input"`
	CacheWriteInputTokens int64 `json:"cache_write_input"`
	OutputTokens          int64 `json:"output"`
	ReasoningTokens       int64 `json:"reasoning"`
}

func (t *Tokens) add(u usageData) {
	t.InputTokens += u.InputTokens
	t.CachedInputTokens += u.CachedInputTokens
	t.CacheWriteInputTokens += u.CacheWriteInputTokens
	t.OutputTokens += u.OutputTokens
	t.ReasoningTokens += u.ReasoningTokens
}

type toolCall struct {
	name, label string
	failed      bool
}

type toolOp struct {
	callID     string
	start, end time.Time
	done       bool
}

type interval struct{ start, end time.Time }

// Tracker consumes runner events, prints progress, and accumulates stats.
type Tracker struct {
	progress io.Writer // nil disables progress output
	verbose  bool
	pal      palette
	origin   time.Time

	first, last time.Time
	turns       int
	responses   int
	tokens      Tokens
	stops       map[string]int
	failures    []string
	errors      []string
	answer      string
	lastText    string
	calls       map[string]*toolCall
	callOrder   []string
	ops         map[string]*toolOp
	turnStart   time.Time
	inTurn      bool
	model       []interval
	closedAt    time.Time
}

func NewTracker(progress io.Writer, verbose bool, pal palette) *Tracker {
	return &Tracker{
		progress: progress,
		verbose:  verbose,
		pal:      pal,
		origin:   time.Now(),
		stops:    map[string]int{},
		calls:    map[string]*toolCall{},
		ops:      map[string]*toolOp{},
	}
}

func (t *Tracker) HandleLine(line []byte) {
	var ev rawEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		t.errors = append(t.errors, "unparseable runner output: "+oneLine(string(line), 200))
		return
	}
	if ev.Type == "error" {
		t.errors = append(t.errors, ev.Message)
		t.say(t.pal.red("error: ") + ev.Message)
		return
	}
	if !ev.RecordedAt.IsZero() {
		if t.first.IsZero() {
			t.first = ev.RecordedAt
		}
		t.last = ev.RecordedAt
	}
	switch ev.Kind {
	case "turn":
		t.turns++
		t.turnStart, t.inTurn = ev.RecordedAt, true
	case "model_response":
		t.onResponse(ev)
	case "tool_call_status":
		t.onToolStatus(ev)
	}
}

func (t *Tracker) onResponse(ev rawEvent) {
	var d modelResponse
	if err := json.Unmarshal(ev.Data, &d); err != nil {
		t.errors = append(t.errors, "decode model_response: "+err.Error())
		return
	}
	resp := d.Response
	t.responses++
	t.inTurn = false
	t.tokens.add(resp.Usage)
	took := ""
	if !t.turnStart.IsZero() {
		t.model = append(t.model, interval{t.turnStart, ev.RecordedAt})
		took = fmt.Sprintf("  %.1fs", ev.RecordedAt.Sub(t.turnStart).Seconds())
	}
	t.say(t.pal.bold(fmt.Sprintf("turn %d", t.turns)) + t.pal.dim(fmt.Sprintf("  %s in · %s out%s",
		commas(resp.Usage.InputTokens), commas(resp.Usage.OutputTokens), took)))
	if resp.Stop != "" && resp.Stop != "complete" {
		t.stops[resp.Stop]++
		t.say(t.pal.yellow("  ! stop: " + resp.Stop))
	}
	if resp.Failure != nil {
		msg := strings.TrimSpace(resp.Failure.Code + ": " + resp.Failure.Message)
		t.failures = append(t.failures, msg)
		t.say(t.pal.red("  ! failure: " + msg))
	}
	for _, item := range resp.Output {
		switch item.Type {
		case "tool_call":
			var c toolCallData
			if json.Unmarshal(item.Data, &c) != nil {
				continue
			}
			tc := &toolCall{name: c.Name, label: describeCall(c)}
			t.calls[c.CallID] = tc
			t.callOrder = append(t.callOrder, c.CallID)
			t.say(fmt.Sprintf("  → %s  %s", t.pal.cyan(tc.name), tc.label))
		case "message":
			var m messageData
			if json.Unmarshal(item.Data, &m) != nil || m.Role != "assistant" {
				continue
			}
			t.lastText = m.Text
			if m.Phase == "final_answer" {
				t.answer = m.Text
				t.say(t.pal.green("  ✓ final answer") + t.pal.dim(fmt.Sprintf(" (%d chars)", len(m.Text))))
			} else if strings.TrimSpace(m.Text) != "" {
				t.say("  · " + oneLine(m.Text, 160))
			}
		case "reasoning":
			if !t.verbose {
				continue
			}
			var r reasoningData
			if json.Unmarshal(item.Data, &r) != nil {
				continue
			}
			for _, s := range r.Summary {
				t.say(t.pal.dim("  ~ " + oneLine(s, 200)))
			}
		}
	}
}

func (t *Tracker) onToolStatus(ev rawEvent) {
	var d toolCallStatus
	if err := json.Unmarshal(ev.Data, &d); err != nil {
		t.errors = append(t.errors, "decode tool_call_status: "+err.Error())
		return
	}
	c := t.calls[d.CallID]
	if c == nil {
		c = &toolCall{name: "?"}
		t.calls[d.CallID] = c
		t.callOrder = append(t.callOrder, d.CallID)
	}
	if d.Status.Error != "" && !c.failed {
		c.failed = true
		t.say(t.pal.red("  ✗ ") + c.name + "  " + oneLine(d.Status.Error, 160))
	}
	for _, o := range d.Operations {
		op := t.ops[o.ID]
		if op == nil {
			op = &toolOp{callID: d.CallID, start: ev.RecordedAt}
			t.ops[o.ID] = op
		}
		if op.done || !isTerminal(o.Status) {
			continue
		}
		op.done, op.end = true, ev.RecordedAt
		ok := o.Status == "completed"
		detail := o.Status
		if o.Type == "shell" {
			var s shellState
			if json.Unmarshal(o.State, &s) == nil {
				if s.Result != nil {
					detail = fmt.Sprintf("exit %d", s.Result.ExitCode)
					ok = ok && s.Result.ExitCode == 0
				}
				if s.TerminalError != "" {
					ok = false
					detail = oneLine(s.TerminalError, 120)
				}
			}
		}
		if !ok {
			c.failed = true
		}
		mark := t.pal.green("  ← ")
		if !ok {
			mark = t.pal.red("  ✗ ")
		}
		t.say(fmt.Sprintf("%s%s  %s %s", mark, c.name, c.label,
			t.pal.dim(fmt.Sprintf("(%s, %.1fs)", detail, op.end.Sub(op.start).Seconds()))))
	}
}

func isTerminal(status string) bool {
	return status == "completed" || status == "failed" || status == "canceled"
}

func (t *Tracker) say(msg string) {
	if t.progress == nil {
		return
	}
	fmt.Fprintf(t.progress, "%s %s\n", t.pal.dim(fmt.Sprintf("[%6.1fs]", time.Since(t.origin).Seconds())), msg)
}

// Close marks when the run ended, so in-flight model turns and tools that
// never finished (timeout, kill) are counted up to that point.
func (t *Tracker) Close(at time.Time) { t.closedAt = at }

// HasError reports whether the run produced a runner error or model failure.
func (t *Tracker) HasError() bool { return len(t.errors) > 0 || len(t.failures) > 0 }

// Answer returns the final answer, falling back to the last assistant message.
func (t *Tracker) Answer() string {
	if t.answer != "" {
		return t.answer
	}
	return t.lastText
}

// Stats is the comparable part of a run summary, derived purely from events.
type Stats struct {
	EventSeconds     float64        `json:"event_seconds"`
	ModelSeconds     float64        `json:"model_seconds"`
	ToolBusySeconds  float64        `json:"tool_busy_seconds"`
	OverlapSeconds   float64        `json:"tool_overlap_with_model_seconds"`
	Turns            int            `json:"turns"`
	ModelResponses   int            `json:"model_responses"`
	ToolCalls        int            `json:"tool_calls"`
	FailedToolCalls  int            `json:"failed_tool_calls"`
	MaxParallelTools int            `json:"max_parallel_tools"`
	ToolsByName      map[string]int `json:"tools_by_name"`
	Tokens           Tokens         `json:"tokens"`
	StopReasons      map[string]int `json:"stop_reasons,omitempty"`
	Failures         []string       `json:"failures,omitempty"`
	Errors           []string       `json:"errors,omitempty"`
	FinalAnswer      bool           `json:"final_answer"`
}

func (t *Tracker) Stats() Stats {
	s := Stats{
		Turns:          t.turns,
		ModelResponses: t.responses,
		ToolCalls:      len(t.callOrder),
		ToolsByName:    map[string]int{},
		Tokens:         t.tokens,
		StopReasons:    t.stops,
		Failures:       t.failures,
		Errors:         t.errors,
		FinalAnswer:    t.answer != "",
	}
	if !t.first.IsZero() {
		s.EventSeconds = t.last.Sub(t.first).Seconds()
	}
	for _, id := range t.callOrder {
		c := t.calls[id]
		s.ToolsByName[c.name]++
		if c.failed {
			s.FailedToolCalls++
		}
	}
	openEnd := later(t.last, t.closedAt)
	var tools []interval
	for _, op := range t.ops {
		end := op.end
		if !op.done {
			end = openEnd
		}
		tools = append(tools, interval{op.start, end})
	}
	modelIntervals := t.model
	if t.inTurn {
		modelIntervals = append(slices.Clone(t.model), interval{t.turnStart, openEnd})
	}
	model := merge(modelIntervals)
	toolUnion := merge(tools)
	s.ModelSeconds = total(model).Seconds()
	s.ToolBusySeconds = total(toolUnion).Seconds()
	s.OverlapSeconds = intersect(model, toolUnion).Seconds()
	s.MaxParallelTools = maxConcurrent(tools)
	return s
}

func merge(in []interval) []interval {
	sorted := slices.Clone(in)
	slices.SortFunc(sorted, func(a, b interval) int { return a.start.Compare(b.start) })
	var out []interval
	for _, iv := range sorted {
		if n := len(out); n > 0 && !iv.start.After(out[n-1].end) {
			if iv.end.After(out[n-1].end) {
				out[n-1].end = iv.end
			}
			continue
		}
		out = append(out, iv)
	}
	return out
}

func total(in []interval) time.Duration {
	var d time.Duration
	for _, iv := range in {
		d += iv.end.Sub(iv.start)
	}
	return d
}

// intersect returns the overlap length of two merged interval lists.
func intersect(a, b []interval) time.Duration {
	var d time.Duration
	for i, j := 0, 0; i < len(a) && j < len(b); {
		start, end := later(a[i].start, b[j].start), earlier(a[i].end, b[j].end)
		if end.After(start) {
			d += end.Sub(start)
		}
		if a[i].end.Before(b[j].end) {
			i++
		} else {
			j++
		}
	}
	return d
}

func maxConcurrent(in []interval) int {
	type edge struct {
		at    time.Time
		delta int
	}
	var edges []edge
	for _, iv := range in {
		edges = append(edges, edge{iv.start, 1}, edge{iv.end, -1})
	}
	// Ends sort before starts at the same instant so back-to-back ops don't count as parallel.
	slices.SortFunc(edges, func(a, b edge) int {
		if c := a.at.Compare(b.at); c != 0 {
			return c
		}
		return a.delta - b.delta
	})
	cur, best := 0, 0
	for _, e := range edges {
		cur += e.delta
		best = max(best, cur)
	}
	return best
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func earlier(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func describeCall(c toolCallData) string {
	if c.Name == "Bash" {
		var args struct{ Command string }
		if json.Unmarshal([]byte(c.Arguments), &args) == nil && args.Command != "" {
			return oneLine(args.Command, 120)
		}
	}
	return oneLine(c.Arguments, 120)
}

func oneLine(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > limit {
		return string(r[:limit-1]) + "…"
	}
	return s
}
