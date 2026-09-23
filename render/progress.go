package render

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/viktordanov/uagent/domain"
)

// Progress is a domain.EventSink that prints one line per notable event.
type Progress struct {
	w       io.Writer
	pal     Palette
	verbose bool
	origin  time.Time
}

func NewProgress(w io.Writer, pal Palette, verbose bool) *Progress {
	return &Progress{w: w, pal: pal, verbose: verbose, origin: time.Now()}
}

func (p *Progress) Emit(event domain.Event) {
	pal := p.pal
	switch e := event.(type) {
	case domain.RunStarted:
		p.origin = e.At
		fmt.Fprintf(p.w, "%s %s/%s · effort %s · %s\n%s\n",
			pal.Bold("uagent"), e.Provider, ModelLabel(e.Model), e.Effort, e.Workspace, pal.Dim("run "+e.RunID))
	case domain.PreflightWarning:
		p.say(pal.Yellow("warning: ") + e.Message)
	case domain.ModelResponded:
		p.say(pal.Bold(fmt.Sprintf("turn %d", e.Turn)) + pal.Dim(fmt.Sprintf("  %s in · %s out  %.1fs",
			Commas(e.Usage.InputTokens), Commas(e.Usage.OutputTokens), e.Duration.Seconds())))
		if e.Stop != "" && e.Stop != "complete" {
			p.say(pal.Yellow("  ! stop: " + e.Stop))
		}
		if e.Failure != "" {
			p.say(pal.Red("  ! failure: " + e.Failure))
		}
	case domain.ToolCalled:
		p.say(fmt.Sprintf("  → %s  %s", pal.Cyan(e.Name), e.Label))
	case domain.ToolFinished:
		mark := pal.Green("  ← ")
		if !e.OK {
			mark = pal.Red("  ✗ ")
		}
		p.say(fmt.Sprintf("%s%s  %s %s", mark, e.Name, e.Label, pal.Dim(fmt.Sprintf("(%s, %.1fs)", e.Detail, e.Duration.Seconds()))))
	case domain.AssistantMessage:
		switch {
		case e.Final:
			p.say(pal.Green("  ✓ final answer") + pal.Dim(fmt.Sprintf(" (%d chars)", len(e.Text))))
		case strings.TrimSpace(e.Text) != "":
			p.say("  · " + oneLine(e.Text, 160))
		}
	case domain.ReasoningSummary:
		if p.verbose {
			p.say(pal.Dim("  ~ " + oneLine(e.Text, 200)))
		}
	case domain.RunnerError:
		p.say(pal.Red("error: ") + e.Message)
	}
}

func (p *Progress) say(msg string) {
	fmt.Fprintf(p.w, "%s %s\n", p.pal.Dim(fmt.Sprintf("[%6.1fs]", time.Since(p.origin).Seconds())), msg)
}

func oneLine(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > limit {
		return string(r[:limit-1]) + "…"
	}

	return s
}
