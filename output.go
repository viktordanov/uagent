package main

import (
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
)

type palette struct{ on bool }

func newPalette(f *os.File) palette {
	if os.Getenv("NO_COLOR") != "" {
		return palette{}
	}
	info, err := f.Stat()
	return palette{on: err == nil && info.Mode()&os.ModeCharDevice != 0}
}

func (p palette) wrap(code, s string) string {
	if !p.on {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (p palette) bold(s string) string   { return p.wrap("1", s) }
func (p palette) dim(s string) string    { return p.wrap("2", s) }
func (p palette) red(s string) string    { return p.wrap("31", s) }
func (p palette) green(s string) string  { return p.wrap("32", s) }
func (p palette) yellow(s string) string { return p.wrap("33", s) }
func (p palette) cyan(s string) string   { return p.wrap("36", s) }

// Summary is written to <run>/summary.json and printed by -json.
type Summary struct {
	Status         string  `json:"status"`
	ExitCode       int     `json:"exit_code"`
	RunnerExitCode int     `json:"runner_exit_code"`
	Provider       string  `json:"provider,omitempty"`
	Model          string  `json:"model,omitempty"`
	Effort         string  `json:"effort,omitempty"`
	SessionID      string  `json:"session_id,omitempty"`
	Workspace      string  `json:"workspace,omitempty"`
	RunDir         string  `json:"run_dir,omitempty"`
	StartedAt      string  `json:"started_at,omitempty"`
	WallSeconds    float64 `json:"wall_seconds"`
	Stats
	Answer string `json:"answer"`
}

func printSummary(w io.Writer, s Summary, pal palette) {
	row := func(label, value string) { fmt.Fprintf(w, "%s %s\n", pal.dim(fmt.Sprintf("%-8s", label)), value) }

	fmt.Fprintln(w, pal.dim("──────── uagent summary ────────"))
	status := s.Status
	switch s.Status {
	case "ok":
		status = pal.green(status)
	default:
		status = pal.red(status)
	}
	row("status", fmt.Sprintf("%s · exit %d · runner exit %d", status, s.ExitCode, s.RunnerExitCode))
	if s.Model != "" {
		row("model", fmt.Sprintf("%s/%s · effort %s", s.Provider, s.Model, s.Effort))
	}
	row("time", fmt.Sprintf("%s wall · model %s · tools busy %s (%s overlapped with model)",
		secs(s.WallSeconds), secs(s.ModelSeconds), secs(s.ToolBusySeconds), secs(s.OverlapSeconds)))
	row("turns", fmt.Sprintf("%d · tool calls %d%s · %d failed · max %d parallel",
		s.Turns, s.ToolCalls, byName(s.ToolsByName), s.FailedToolCalls, s.MaxParallelTools))
	t := s.Tokens
	row("tokens", fmt.Sprintf("%s in (%s cached) · %s out (%s reasoning) · %s total",
		commas(t.InputTokens), commas(t.CachedInputTokens), commas(t.OutputTokens),
		commas(t.ReasoningTokens), commas(t.InputTokens+t.OutputTokens)))
	for reason, n := range s.StopReasons {
		row("stop", pal.yellow(fmt.Sprintf("%s ×%d", reason, n)))
	}
	for _, f := range s.Failures {
		row("failure", pal.red(f))
	}
	for _, e := range s.Errors {
		row("error", pal.red(e))
	}
	switch {
	case s.FinalAnswer:
	case s.Answer != "":
		row("answer", pal.yellow("no final_answer message; printed last assistant text"))
	default:
		row("answer", pal.yellow("none"))
	}
	if s.SessionID != "" {
		row("session", s.SessionID)
	}
	if s.RunDir != "" {
		row("run", s.RunDir)
	}
}

func byName(m map[string]int) string {
	if len(m) == 0 {
		return ""
	}
	var parts []string
	for _, name := range slices.Sorted(maps.Keys(m)) {
		parts = append(parts, fmt.Sprintf("%s %d", name, m[name]))
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

func secs(s float64) string {
	if s >= 120 {
		return fmt.Sprintf("%dm%02ds", int(s)/60, int(s)%60)
	}
	return fmt.Sprintf("%.1fs", s)
}

func commas(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%c", float64(n)/float64(div), "KMGTPE"[exp])
}
