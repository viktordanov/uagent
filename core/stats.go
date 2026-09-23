package core

import (
	"slices"
	"time"
)

// Tokens is summed model usage. InputTokens includes cached and cache-write
// tokens; OutputTokens includes reasoning tokens.
type Tokens struct {
	InputTokens           int64
	CachedInputTokens     int64
	CacheWriteInputTokens int64
	OutputTokens          int64
	ReasoningTokens       int64
}

func (t Tokens) Add(u Tokens) Tokens {
	return Tokens{
		InputTokens:           t.InputTokens + u.InputTokens,
		CachedInputTokens:     t.CachedInputTokens + u.CachedInputTokens,
		CacheWriteInputTokens: t.CacheWriteInputTokens + u.CacheWriteInputTokens,
		OutputTokens:          t.OutputTokens + u.OutputTokens,
		ReasoningTokens:       t.ReasoningTokens + u.ReasoningTokens,
	}
}

// Stats is the comparable part of a run, derived only from events.
type Stats struct {
	// EventSpan is the time between the first and last runner event.
	EventSpan        time.Duration
	ModelTime        time.Duration
	ToolBusyTime     time.Duration
	ToolModelOverlap time.Duration
	Turns            int
	UserMessages     int
	ModelResponses   int
	ToolCalls        int
	FailedToolCalls  int
	MaxParallelTools int
	ToolsByName      map[string]int
	Tokens           Tokens
	StopReasons      map[string]int
	Failures         []string
	Errors           []string
	Warnings         []string
	FinalAnswer      bool
}

type interval struct{ start, end time.Time }

type toolSpan struct {
	start, end time.Time
	done       bool
}

// StatsCollector folds run events into Stats. It is not safe for concurrent use.
type StatsCollector struct {
	first, last time.Time
	closedAt    time.Time
	stats       Stats
	answer      string
	lastText    string
	callNames   map[string]string
	callOrder   []string
	failedCalls map[string]bool
	ops         map[string]*toolSpan
	turnStart   time.Time
	inTurn      bool
	model       []interval
}

func NewStatsCollector() *StatsCollector {
	return &StatsCollector{
		stats: Stats{
			ToolsByName: map[string]int{},
			StopReasons: map[string]int{},
		},
		callNames:   map[string]string{},
		failedCalls: map[string]bool{},
		ops:         map[string]*toolSpan{},
	}
}

func (c *StatsCollector) Add(event Event) {
	switch e := event.(type) {
	case TurnStarted:
		c.observe(e.At)
		c.stats.Turns++
		c.turnStart, c.inTurn = e.At, true
	case ModelResponded:
		c.observe(e.At)
		c.stats.ModelResponses++
		c.stats.Tokens = c.stats.Tokens.Add(e.Usage)
		if c.inTurn {
			c.model = append(c.model, interval{c.turnStart, e.At})
			c.inTurn = false
		}
		if e.Stop != "" && e.Stop != "complete" {
			c.stats.StopReasons[e.Stop]++
		}
		if e.Failure != "" {
			c.stats.Failures = append(c.stats.Failures, e.Failure)
		}
	case ToolCalled:
		c.observe(e.At)
		c.trackCall(e.CallID, e.Name)
	case ToolStarted:
		c.observe(e.At)
		c.trackCall(e.CallID, e.Name)
		if _, ok := c.ops[e.OpID]; !ok {
			c.ops[e.OpID] = &toolSpan{start: e.At}
		}
	case ToolFinished:
		c.observe(e.At)
		c.trackCall(e.CallID, e.Name)
		if !e.OK {
			c.failedCalls[e.CallID] = true
		}
		if span, ok := c.ops[e.OpID]; ok && !span.done {
			span.end, span.done = e.At, true
		}
	case AssistantMessage:
		c.observe(e.At)
		c.lastText = e.Text
		if e.Final {
			c.answer = e.Text
		}
	case UserMessage:
		c.observe(e.At)
		c.stats.UserMessages++
	case ControlInput:
		c.observe(e.At)
	case RunnerError:
		c.stats.Errors = append(c.stats.Errors, e.Message)
	case PreflightWarning:
		c.stats.Warnings = append(c.stats.Warnings, e.Message)
	}
}

// Close marks when the run ended so in-flight turns and tools are counted up to it.
func (c *StatsCollector) Close(at time.Time) { c.closedAt = at }

// HasError reports whether the runner reported an error or a model failure.
func (c *StatsCollector) HasError() bool {
	return len(c.stats.Errors) > 0 || len(c.stats.Failures) > 0
}

// Answer returns the final answer, or the last assistant text when there is none.
func (c *StatsCollector) Answer() string {
	if c.answer != "" {
		return c.answer
	}

	return c.lastText
}

func (c *StatsCollector) Stats() Stats {
	s := c.stats
	s.ToolsByName = map[string]int{}
	for _, id := range c.callOrder {
		s.ToolsByName[c.callNames[id]]++
		if c.failedCalls[id] {
			s.FailedToolCalls++
		}
	}
	s.ToolCalls = len(c.callOrder)
	s.FinalAnswer = c.answer != ""
	if !c.first.IsZero() {
		s.EventSpan = c.last.Sub(c.first)
	}

	openEnd := later(c.last, c.closedAt)
	tools := make([]interval, 0, len(c.ops))
	for _, span := range c.ops {
		end := span.end
		if !span.done {
			end = openEnd
		}
		tools = append(tools, interval{span.start, end})
	}
	modelIntervals := c.model
	if c.inTurn {
		modelIntervals = append(slices.Clone(c.model), interval{c.turnStart, openEnd})
	}
	model, toolUnion := merge(modelIntervals), merge(tools)
	s.ModelTime = total(model)
	s.ToolBusyTime = total(toolUnion)
	s.ToolModelOverlap = intersect(model, toolUnion)
	s.MaxParallelTools = maxConcurrent(tools)

	return s
}

func (c *StatsCollector) observe(at time.Time) {
	if at.IsZero() {
		return
	}
	if c.first.IsZero() {
		c.first = at
	}
	c.last = later(c.last, at)
}

func (c *StatsCollector) trackCall(callID, name string) {
	if _, ok := c.callNames[callID]; ok {
		return
	}
	if name == "" {
		name = "?"
	}
	c.callNames[callID] = name
	c.callOrder = append(c.callOrder, callID)
}

func merge(in []interval) []interval {
	sorted := slices.Clone(in)
	slices.SortFunc(sorted, func(a, b interval) int { return a.start.Compare(b.start) })
	var out []interval
	for _, iv := range sorted {
		if n := len(out); n > 0 && !iv.start.After(out[n-1].end) {
			out[n-1].end = later(out[n-1].end, iv.end)

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
	edges := make([]edge, 0, 2*len(in))
	for _, iv := range in {
		edges = append(edges, edge{iv.start, 1}, edge{iv.end, -1})
	}
	// Ends sort before starts at the same instant so back-to-back tools don't count as parallel.
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
