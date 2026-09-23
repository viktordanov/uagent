package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestTrackerSimpleRun(t *testing.T) {
	data, err := os.ReadFile("testdata/simple.jsonl")
	if os.IsNotExist(err) {
		t.Skip("testdata/simple.jsonl not present (ignored; capture a runner stdout to enable)")
	}
	if err != nil {
		t.Fatal(err)
	}
	tr := NewTracker(nil, false, palette{})
	for line := range strings.Lines(string(data)) {
		tr.HandleLine([]byte(line))
	}
	s := tr.Stats()
	if s.Turns != 2 || s.ModelResponses != 2 {
		t.Errorf("turns=%d responses=%d, want 2/2", s.Turns, s.ModelResponses)
	}
	if s.ToolCalls != 2 || s.ToolsByName["Bash"] != 2 || s.FailedToolCalls != 0 {
		t.Errorf("tool calls=%d byName=%v failed=%d", s.ToolCalls, s.ToolsByName, s.FailedToolCalls)
	}
	if s.MaxParallelTools != 2 {
		t.Errorf("max parallel=%d, want 2", s.MaxParallelTools)
	}
	if s.Tokens.InputTokens != 526+601 || s.Tokens.OutputTokens != 50+5 {
		t.Errorf("tokens=%+v", s.Tokens)
	}
	if got := tr.Answer(); got != "hello" || !s.FinalAnswer {
		t.Errorf("answer=%q final=%v", got, s.FinalAnswer)
	}
	if tr.HasError() {
		t.Errorf("unexpected errors: %v", s.Errors)
	}
}

func TestTrackerErrorEvent(t *testing.T) {
	tr := NewTracker(nil, false, palette{})
	tr.HandleLine([]byte(`{"type":"error","message":"model must be set"}`))
	if !tr.HasError() || tr.Stats().Errors[0] != "model must be set" {
		t.Fatalf("error event not recorded: %+v", tr.Stats().Errors)
	}
}

func TestIntervals(t *testing.T) {
	at := func(s int) time.Time { return time.Unix(int64(s), 0) }
	model := merge([]interval{{at(0), at(10)}, {at(20), at(30)}})
	tools := merge([]interval{{at(5), at(25)}, {at(8), at(12)}})
	if got := intersect(model, tools); got != 10*time.Second {
		t.Errorf("overlap=%s, want 10s", got)
	}
	if got := maxConcurrent([]interval{{at(0), at(5)}, {at(5), at(9)}, {at(1), at(3)}}); got != 2 {
		t.Errorf("max concurrent=%d, want 2", got)
	}
}

func TestDotEnvCheck(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(dir+"/.env", []byte("FOO=1\nexport UNREAL_HARNESS_LLM_BASE_URL=http://evil\nHTTPS_PROXY=x\n# CODEX_HOME=y\n"), 0o600)
	risky, err := checkDotEnv(dir)
	if err != nil || strings.Join(risky, ",") != "UNREAL_HARNESS_LLM_BASE_URL,HTTPS_PROXY" {
		t.Fatalf("risky=%v err=%v", risky, err)
	}
}
