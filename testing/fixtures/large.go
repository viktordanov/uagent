package fixtures

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// LargeRunnerOutput returns a synthetic runner stdout of turns turns in the
// shape of parallel.jsonl, for benchmarks. Each turn has a settings input, a
// prompt, a model response with reasoning and three Bash calls, three status
// lines per call (ready, running, completed with 2 to 32 KB of output, every
// tenth call failing), and a closing message; the last one is the final answer.
// The output is the same for the same turns.
func LargeRunnerOutput(turns int) []byte {
	g := largeOutput{at: T0}
	for t := range turns {
		g.turn(t, t == turns-1)
	}

	return g.buf.Bytes()
}

type largeOutput struct {
	buf bytes.Buffer
	seq int
	at  time.Time
}

func (g *largeOutput) turn(t int, last bool) {
	turnID := fmt.Sprintf("00000000-0000-4000-8000-%012d", t)
	g.item("input", `{"ID":%s,"Kind":"control","Payload":{"Mode":"settings","Reason":"","Parameters":{"ReasoningEffort":"high"}}}`, q(fmt.Sprintf("in-%d-settings", t)))
	g.item("input", `{"ID":%s,"Kind":"external","Payload":%s}`, q(fmt.Sprintf("in-%d-prompt", t)), q(fmt.Sprintf("Check how step %d validates requests and note it down.", t)))
	g.item("turn", `{"ID":%s,"PreviousTurnID":"","Type":"regular"}`, q(turnID))
	g.item("input", `{"ID":%s,"Kind":"control","Payload":{"Mode":"when_idle","Reason":""}}`, q(fmt.Sprintf("in-%d-idle", t)))
	var calls []string
	outputs := []string{fmt.Sprintf(`{"ProviderID":"rs-%d","Type":"reasoning","Data":{"Summary":[%s]}}`, t, q(fmt.Sprintf("**Reading step %d**\n\nI'll look at the handler and its tests first.", t)))}
	for c := range 3 {
		callID := fmt.Sprintf("call-%d-%d", t, c)
		calls = append(calls, callID)
		args := `{"command":` + q(fmt.Sprintf("rg -n 'Validate' internal/server/step%d_%d.go", t, c)) + `}`
		outputs = append(outputs, fmt.Sprintf(`{"ProviderID":"fc-%d-%d","Type":"tool_call","Data":{"CallID":%s,"Name":"Bash","Arguments":%s}}`, t, c, q(callID), q(args)))
	}
	g.response(turnID, t, outputs)
	for c, callID := range calls {
		g.status(turnID, callID, t*3+c)
	}
	phase := "commentary"
	if last {
		phase = "final_answer"
	}
	g.response(turnID, t, []string{fmt.Sprintf(`{"ProviderID":"msg-%d","Type":"message","Data":{"Role":"assistant","Text":%s,"Phase":%q}}`, t, q(fmt.Sprintf("Step %d validates each request before it reaches the store.", t)), phase)})
}

func (g *largeOutput) response(turnID string, t int, outputs []string) {
	g.item("model_response", `{"TurnID":%s,"Response":{"ID":"resp-%d","Stop":"complete","Output":[%s],"Usage":{"InputTokens":%d,"CachedInputTokens":%d,"CacheWriteInputTokens":0,"OutputTokens":%d,"ReasoningTokens":%d,"Raw":{"input_tokens_details":{"cache_write_tokens":0,"cached_tokens":%d},"output_tokens_details":{"reasoning_tokens":%d},"total_tokens":%d}},"Failure":null}}`,
		q(turnID), t, strings.Join(outputs, ","), 20000+t*900, 18000+t*900, 300, 120, 18000+t*900, 120, 20300+t*900)
}

// status writes the ready, running, and completed lines of one shell operation.
func (g *largeOutput) status(turnID, callID string, n int) {
	opID := fmt.Sprintf("10000000-0000-4000-8000-%012d", n)
	base := "/state/sessions/operations/" + opID
	out := commandOutput(n)
	exit := 0
	if n%10 == 9 {
		exit = 1
	}
	for _, phase := range []struct{ status, inline, result string }{
		{"ready", `""`, "null"},
		{"running", q(out[:len(out)/2]), "null"},
		{"completed", `""`, fmt.Sprintf(`{"Out":%s,"Err":"","OutSize":%d,"ErrSize":0,"ExitCode":%d}`, q(out), len(out), exit)},
	} {
		g.item("tool_call_status", `{"TurnID":%s,"CallID":%s,"Status":{"Error":"","WaitingFor":[%s]},"Operations":[{"MaxOutputLength":40000,"ID":%s,"Type":"shell","Version":3,"Status":%q,"State":{"Input":{"Command":"rg -n 'Validate'","Shell":"/bin/zsh","Directory":"/workspace"},"BaseDirectory":%s,"Phase":"","ProcessGroupID":0,"PendingExitCode":null,"OutSize":%d,"ErrSize":0,"InlineOut":%s,"InlineErr":"","InlineOutTail":"","InlineErrTail":"","Result":%s,"TerminalError":"","ErrorTruncated":false,"OutTruncated":false,"ErrTruncated":false,"OutPath":%s,"ErrPath":%s}}]}`,
			q(turnID), q(callID), q(opID), q(opID), phase.status, q(base), len(out), phase.inline, phase.result, q(base+"/out"), q(base+"/err"))
	}
}

func (g *largeOutput) item(kind, data string, args ...any) {
	g.seq++
	g.at = g.at.Add(7 * time.Millisecond)
	fmt.Fprintf(&g.buf, `{"Sequence":%d,"RecordedAt":%q,"Kind":%q,"Data":`, g.seq, g.at.Format(time.RFC3339Nano), kind)
	fmt.Fprintf(&g.buf, data, args...)
	g.buf.WriteString("}\n")
}

// commandOutput returns 2 to 32 KB of search results with tabs, quotes, and non-ASCII text.
func commandOutput(n int) string {
	var b strings.Builder
	size := 2048 << (n % 5)
	for i := 0; b.Len() < size; i++ {
		fmt.Fprintf(&b, "internal/server/step%d.go:%d:\tif err := h.Validate(r); err != nil { return fmt.Errorf(\"validate → %%w\", err) }\n", n, i+1)
	}

	return b.String()
}

func q(s string) string {
	data, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}

	return string(data)
}
