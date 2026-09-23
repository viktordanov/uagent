package core_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/viktordanov/uagent/core"
	"github.com/viktordanov/uagent/testing/fixtures"
)

func collect(events ...[]core.Event) *core.StatsCollector {
	c := core.NewStatsCollector()
	for _, group := range events {
		for _, e := range group {
			c.Add(e)
		}
	}

	return c
}

func TestStatsCollector(t *testing.T) {
	usage := core.Tokens{InputTokens: 100, CachedInputTokens: 40, OutputTokens: 10, ReasoningTokens: 3}

	t.Run("counts turns, tokens, and tools", func(t *testing.T) {
		c := collect(
			fixtures.Turn(1, 0, 2*time.Second, usage),
			fixtures.ToolRun("a", 2*time.Second, time.Second, true),
			fixtures.ToolRun("b", 2*time.Second, time.Second, false),
			fixtures.Turn(2, 4*time.Second, time.Second, usage),
		)

		s := c.Stats()

		assert.Equal(t, 2, s.Turns)
		assert.Equal(t, 2, s.ModelResponses)
		assert.Equal(t, core.Tokens{InputTokens: 200, CachedInputTokens: 80, OutputTokens: 20, ReasoningTokens: 6}, s.Tokens)
		assert.Equal(t, 2, s.ToolCalls)
		assert.Equal(t, 1, s.FailedToolCalls)
		assert.Equal(t, map[string]int{"Bash": 2}, s.ToolsByName)
		assert.Equal(t, 2, s.MaxParallelTools)
		assert.Equal(t, 3*time.Second, s.ModelTime)
		assert.Equal(t, time.Second, s.ToolBusyTime)
		assert.Equal(t, 5*time.Second, s.EventSpan)
	})

	t.Run("measures tool time overlapping model time", func(t *testing.T) {
		// Model 0-10s, tool 5-15s: 5s of the tool ran while the model worked.
		c := collect(
			fixtures.Turn(1, 0, 10*time.Second, usage),
			fixtures.ToolRun("a", 5*time.Second, 10*time.Second, true),
		)

		s := c.Stats()

		assert.Equal(t, 5*time.Second, s.ToolModelOverlap)
		assert.Equal(t, 10*time.Second, s.ToolBusyTime)
	})

	t.Run("back-to-back tools are not parallel", func(t *testing.T) {
		c := collect(
			fixtures.ToolRun("a", 0, time.Second, true),
			fixtures.ToolRun("b", time.Second, time.Second, true),
		)

		assert.Equal(t, 1, c.Stats().MaxParallelTools)
	})

	t.Run("in-flight work counts up to Close", func(t *testing.T) {
		c := collect([]core.Event{
			core.TurnStarted{At: fixtures.At(0), Turn: 1},
			core.ToolStarted{At: fixtures.At(0), CallID: "a", OpID: "op-a", Name: "Bash"},
		})
		c.Close(fixtures.At(8 * time.Second))

		s := c.Stats()

		assert.Equal(t, 8*time.Second, s.ToolBusyTime)
		assert.Equal(t, 8*time.Second, s.ModelTime)
		assert.Equal(t, 1, s.MaxParallelTools)
	})

	t.Run("answer prefers the final message", func(t *testing.T) {
		c := collect([]core.Event{
			core.AssistantMessage{At: fixtures.At(0), Text: "thinking out loud"},
			core.AssistantMessage{At: fixtures.At(time.Second), Text: "final", Final: true},
			core.AssistantMessage{At: fixtures.At(2 * time.Second), Text: "trailing"},
		})

		assert.Equal(t, "final", c.Answer())
		assert.True(t, c.Stats().FinalAnswer)
	})

	t.Run("answer falls back to the last assistant text", func(t *testing.T) {
		c := collect([]core.Event{core.AssistantMessage{At: fixtures.At(0), Text: "partial"}})

		assert.Equal(t, "partial", c.Answer())
		assert.False(t, c.Stats().FinalAnswer)
	})

	t.Run("records errors, failures, and stop reasons", func(t *testing.T) {
		c := collect([]core.Event{
			core.ModelResponded{At: fixtures.At(0), Stop: "max_output_tokens", Failure: "server_error: overloaded"},
			core.RunnerError{At: fixtures.At(0), Message: "model must be set"},
		})

		s := c.Stats()

		assert.True(t, c.HasError())
		assert.Equal(t, map[string]int{"max_output_tokens": 1}, s.StopReasons)
		assert.Equal(t, []string{"server_error: overloaded"}, s.Failures)
		assert.Equal(t, []string{"model must be set"}, s.Errors)
	})
}
