package runner_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/viktordanov/uagent/domain"
	"github.com/viktordanov/uagent/runner"
	"github.com/viktordanov/uagent/testing/fixtures"
)

func readFixture(t *testing.T, name string) ([]domain.Event, *domain.StatsCollector) {
	t.Helper()
	var events []domain.Event
	c := domain.NewStatsCollector()
	err := runner.ReadEvents(bytes.NewReader(fixtures.RunnerOutput(name)), func(e domain.Event) {
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
		assert.Contains(t, events, domain.Event(domain.ToolCalled{
			At: events[1].OccurredAt(), CallID: "call_WeU5T6p7qIYxhEwBoDVehfYG", Name: "Bash", Label: "ls",
		}))
	})

	t.Run("each tool finishes exactly once with its exit code", func(t *testing.T) {
		events, _ := readFixture(t, "parallel.jsonl")

		var finished []domain.ToolFinished
		for _, e := range events {
			if f, ok := e.(domain.ToolFinished); ok {
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
			case domain.ToolStarted:
				started++
			case domain.ToolFinished:
				finished++
			}
		}

		assert.Equal(t, 1, started)
		assert.Equal(t, 0, finished)
		assert.Empty(t, c.Answer())
	})

	t.Run("error and unparseable lines become runner errors", func(t *testing.T) {
		d := runner.NewDecoder()

		errEvents := d.Decode([]byte(`{"type":"error","message":"model must be set"}`))
		junk := d.Decode([]byte("panic: oops"))

		require.Len(t, errEvents, 1)
		assert.Equal(t, "model must be set", errEvents[0].(domain.RunnerError).Message)
		require.Len(t, junk, 1)
		assert.Contains(t, junk[0].(domain.RunnerError).Message, "unparseable runner output: panic: oops")
		assert.Empty(t, d.Decode([]byte("  \n")))
	})
}
