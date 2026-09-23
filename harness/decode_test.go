package harness_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/viktordanov/uagent/core"
	"github.com/viktordanov/uagent/harness"
	"github.com/viktordanov/uagent/testing/fixtures"
)

func readFixture(t *testing.T, name string) ([]core.Event, *core.StatsCollector) {
	t.Helper()
	var events []core.Event
	c := core.NewStatsCollector()
	err := harness.ReadEvents(bytes.NewReader(fixtures.RunnerOutput(name)), func(e core.Event) {
		events = append(events, e)
		c.Add(e)
	})
	require.NoError(t, err)

	return events, c
}

func TestReadEvents(t *testing.T) {
	t.Run("parallel tool calls and a final answer", func(t *testing.T) {
		events, c := readFixture(t, "simple.jsonl")

		s := c.Stats()

		assert.Equal(t, 2, s.Turns)
		assert.Equal(t, 2, s.ToolCalls)
		assert.Equal(t, 2, s.MaxParallelTools)
		assert.Equal(t, 0, s.FailedToolCalls)
		assert.Equal(t, int64(526+601), s.Tokens.InputTokens)
		assert.Equal(t, int64(50+5), s.Tokens.OutputTokens)
		assert.Equal(t, "hello", c.Answer())
		var called []core.ToolCalled
		for _, e := range events {
			if c, ok := e.(core.ToolCalled); ok {
				called = append(called, c)
			}
		}
		require.Len(t, called, 2)
		assert.Equal(t, "call_WeU5T6p7qIYxhEwBoDVehfYG", called[0].CallID)
		assert.Equal(t, "ls", called[0].Label)
		assert.JSONEq(t, `{"command":"ls"}`, called[0].Arguments)
	})

	t.Run("inputs become user messages and control inputs", func(t *testing.T) {
		events, c := readFixture(t, "simple.jsonl")

		controls, messages := []core.ControlInput{}, []core.UserMessage{}
		for _, e := range events {
			switch v := e.(type) {
			case core.ControlInput:
				controls = append(controls, v)
			case core.UserMessage:
				messages = append(messages, v)
			}
		}

		require.Len(t, messages, 1)
		assert.Equal(t, "7cb42beb-329b-4c7b-8c2f-abced68ef095", messages[0].ID)
		assert.Equal(t, "Run `ls` and `cat a.txt`, then reply with the file contents.", messages[0].Text)
		require.Len(t, controls, 2)
		assert.Equal(t, "settings", controls[0].Mode)
		assert.Equal(t, "low", controls[0].Effort)
		assert.Equal(t, "when_idle", controls[1].Mode)
		assert.Equal(t, 1, c.Stats().UserMessages)
	})

	t.Run("turn IDs and turn numbers link responses and messages", func(t *testing.T) {
		events, _ := readFixture(t, "simple.jsonl")

		var answer core.AssistantMessage
		turnIDs := map[int]string{}
		for _, e := range events {
			switch v := e.(type) {
			case core.TurnStarted:
				turnIDs[v.Turn] = v.TurnID
			case core.ModelResponded:
				assert.Equal(t, turnIDs[v.Turn], v.TurnID)
			case core.AssistantMessage:
				answer = v
			}
		}

		assert.Equal(t, 2, answer.Turn)
		assert.Equal(t, "47a2965d-78e3-4f07-9df4-a76d53bcecb4", turnIDs[2])
	})

	t.Run("finished shell tools carry their output files", func(t *testing.T) {
		events, _ := readFixture(t, "simple.jsonl")

		for _, e := range events {
			if f, ok := e.(core.ToolFinished); ok {
				assert.Equal(t, "shell", f.OpType)
				assert.True(t, strings.HasSuffix(f.OutPath, f.OpID+"/out"), f.OutPath)
				assert.True(t, strings.HasSuffix(f.ErrPath, f.OpID+"/err"), f.ErrPath)
			}
		}
	})

	t.Run("each tool finishes exactly once with its exit code", func(t *testing.T) {
		events, _ := readFixture(t, "parallel.jsonl")

		var finished []core.ToolFinished
		for _, e := range events {
			if f, ok := e.(core.ToolFinished); ok {
				finished = append(finished, f)
			}
		}

		require.Len(t, finished, 3)
		for _, f := range finished {
			assert.True(t, f.OK)
			assert.Equal(t, "exit 0", f.Detail)
			assert.Equal(t, "Bash", f.Name)
		}
		assert.Equal(t, "sleep 4; echo A", finished[0].Label)
	})

	t.Run("a killed run leaves the tool unfinished", func(t *testing.T) {
		events, c := readFixture(t, "timeout.jsonl")

		var started, finished int
		for _, e := range events {
			switch e.(type) {
			case core.ToolStarted:
				started++
			case core.ToolFinished:
				finished++
			}
		}

		assert.Equal(t, 1, started)
		assert.Equal(t, 0, finished)
		assert.Empty(t, c.Answer())
	})

	t.Run("error and unparseable lines become runner errors", func(t *testing.T) {
		d := harness.NewDecoder()

		errEvents := d.Decode([]byte(`{"type":"error","message":"model must be set"}`))
		junk := d.Decode([]byte("panic: oops"))

		require.Len(t, errEvents, 1)
		assert.Equal(t, "model must be set", errEvents[0].(core.RunnerError).Message)
		require.Len(t, junk, 1)
		assert.Contains(t, junk[0].(core.RunnerError).Message, "unparseable runner output: panic: oops")
		assert.Empty(t, d.Decode([]byte("  \n")))
	})
}
