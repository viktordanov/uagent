package stream_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/viktordanov/uagent/core"
	"github.com/viktordanov/uagent/stream"
	"github.com/viktordanov/uagent/testing/fixtures"
)

func TestSink_Emit(t *testing.T) {
	t.Run("writes one versioned JSON object per event", func(t *testing.T) {
		var buf bytes.Buffer
		sink := stream.NewSink(&buf)

		sink.Emit(core.TurnStarted{At: fixtures.T0, Turn: 1})
		sink.Emit(core.ToolFinished{At: fixtures.At(time.Second), CallID: "c1", OpID: "o1", Name: "Bash", Label: "ls", OK: true, Detail: "exit 0", Duration: 1500 * time.Millisecond})

		require.NoError(t, sink.Err())
		lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
		require.Len(t, lines, 2)
		assert.JSONEq(t, `{"v":1,"type":"turn_started","at":"2026-09-23T12:00:00Z","turn":1}`, lines[0])
		assert.JSONEq(t, `{"v":1,"type":"tool_finished","at":"2026-09-23T12:00:01Z","call_id":"c1","op_id":"o1","name":"Bash","label":"ls","ok":true,"detail":"exit 0","duration_ms":1500}`, lines[1])
	})

	t.Run("run_finished carries the summary", func(t *testing.T) {
		var buf bytes.Buffer
		result := core.Result{
			Request: fixtures.Request(), Status: core.StatusOK, StartedAt: fixtures.T0, Wall: 2 * time.Second,
			Stats: core.Stats{Turns: 1, ToolsByName: map[string]int{"Bash": 1}, Tokens: core.Tokens{InputTokens: 10}}, Answer: "hi",
		}

		stream.NewSink(&buf).Emit(core.RunFinished{At: fixtures.At(2 * time.Second), Result: result})

		var got struct {
			Type    string            `json:"type"`
			Summary stream.SummaryDTO `json:"summary"`
		}
		require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
		assert.Equal(t, "run_finished", got.Type)
		assert.Equal(t, "ok", got.Summary.Status)
		assert.Equal(t, fixtures.RunID, got.Summary.RunID)
		assert.Equal(t, int64(2000), got.Summary.WallMS)
		assert.Equal(t, int64(10), got.Summary.Stats.Tokens.Input)
		assert.Equal(t, "hi", got.Summary.Answer)
	})
}
