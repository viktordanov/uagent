package main

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/viktordanov/uagent/core"
)

// Summary prints the end-of-run summary block.
func Summary(w io.Writer, r core.Result, runDir string, pal Palette) {
	row := func(label, value string) { fmt.Fprintf(w, "%s %s\n", pal.Dim(fmt.Sprintf("%-8s", label)), value) }
	s := r.Stats

	fmt.Fprintln(w, pal.Dim("──────── uagent summary ────────"))
	status := pal.Red(string(r.Status))
	if r.Status == core.StatusOK {
		status = pal.Green(string(r.Status))
	}
	row("status", fmt.Sprintf("%s · runner exit %d", status, r.RunnerExitCode))
	if r.Request.Provider != "" {
		row("model", fmt.Sprintf("%s/%s · effort %s", r.Request.Provider, ModelLabel(r.Request.Model), r.Request.Effort))
	}
	row("time", fmt.Sprintf("%s wall · model %s · tools busy %s (%s overlapped with model)",
		Duration(r.Wall), Duration(s.ModelTime), Duration(s.ToolBusyTime), Duration(s.ToolModelOverlap)))
	row("turns", fmt.Sprintf("%d · tool calls %d%s · %d failed · max %d parallel",
		s.Turns, s.ToolCalls, byName(s.ToolsByName), s.FailedToolCalls, s.MaxParallelTools))
	t := s.Tokens
	row("tokens", fmt.Sprintf("%s in (%s cached) · %s out (%s reasoning) · %s total",
		Commas(t.InputTokens), Commas(t.CachedInputTokens), Commas(t.OutputTokens),
		Commas(t.ReasoningTokens), Commas(t.InputTokens+t.OutputTokens)))
	for _, reason := range slices.Sorted(maps.Keys(s.StopReasons)) {
		row("stop", pal.Yellow(fmt.Sprintf("%s ×%d", reason, s.StopReasons[reason])))
	}
	for _, f := range s.Failures {
		row("failure", pal.Red(f))
	}
	for _, e := range s.Errors {
		row("error", pal.Red(e))
	}
	switch {
	case s.FinalAnswer:
	case r.Answer != "":
		row("answer", pal.Yellow("no final_answer message; printed last assistant text"))
	default:
		row("answer", pal.Yellow("none"))
	}
	if r.Request.SessionID != "" {
		row("session", r.Request.SessionID)
	}
	if runDir != "" {
		row("run", runDir)
	}
}

// ModelLabel shows an empty model as the provider default.
func ModelLabel(model string) string {
	if model == "" {
		return "(provider default)"
	}

	return model
}

func byName(m map[string]int) string {
	if len(m) == 0 {
		return ""
	}
	parts := make([]string, 0, len(m))
	for _, name := range slices.Sorted(maps.Keys(m)) {
		parts = append(parts, fmt.Sprintf("%s %d", name, m[name]))
	}

	return " (" + strings.Join(parts, ", ") + ")"
}

// Duration prints seconds with one decimal, or minutes and seconds from two minutes up.
func Duration(d time.Duration) string {
	if d >= 2*time.Minute {
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}

	return fmt.Sprintf("%.1fs", d.Seconds())
}

// Commas formats n with thousands separators.
func Commas(n int64) string {
	s := strconv.FormatInt(n, 10)
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}

	return sign + b.String()
}
