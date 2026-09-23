package harness_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/viktordanov/uagent/core"
	"github.com/viktordanov/uagent/harness"
	"github.com/viktordanov/uagent/stream"
	"github.com/viktordanov/uagent/testing/fixtures"
)

var update = flag.Bool("update", false, "rewrite golden files")

var fakeRunner string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "uagent-fakerunner")
	if err != nil {
		panic(err)
	}
	fakeRunner = filepath.Join(dir, "fakerunner")
	build := exec.Command("go", "build", "-o", fakeRunner, "github.com/viktordanov/uagent/testing/fakerunner")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build fakerunner: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// testEnv is one isolated uagent setup: a workspace, a state directory, valid
// Codex credentials, and the fake runner replaying a fixture.
type testEnv struct {
	h         *harness.Harness
	stateDir  string
	workspace string
	codexHome string
	capture   string
	events    []core.Event
	stream    bytes.Buffer
}

func newTestEnv(t *testing.T, fixture string) *testEnv {
	t.Helper()
	root := t.TempDir()
	e := &testEnv{
		stateDir:  filepath.Join(root, "state"),
		workspace: filepath.Join(root, "workspace"),
		codexHome: filepath.Join(root, "codex"),
		capture:   filepath.Join(root, "capture"),
	}
	for _, dir := range []string{e.workspace, e.codexHome, e.capture} {
		require.NoError(t, os.MkdirAll(dir, 0o700))
	}
	e.writeCodexToken(t, 24*time.Hour)
	t.Setenv("FAKERUNNER_FIXTURE", fixtures.Path(fixture))
	t.Setenv("FAKERUNNER_CAPTURE", e.capture)
	e.h = harness.New(harness.Config{
		RunnerPath: fakeRunner,
		StateDir:   e.stateDir,
		KillGrace:  300 * time.Millisecond,
		Getenv: func(k string) string {
			if k == "CODEX_HOME" {
				return e.codexHome
			}

			return ""
		},
	})

	return e
}

func (e *testEnv) writeCodexToken(t *testing.T, expiresIn time.Duration) {
	t.Helper()
	claims := `{"exp":` + strconv.FormatInt(time.Now().Add(expiresIn).Unix(), 10) + `}`
	auth := `{"tokens":{"access_token":"x.` + base64.RawURLEncoding.EncodeToString([]byte(claims)) + `.y"}}`
	require.NoError(t, os.WriteFile(filepath.Join(e.codexHome, "auth.json"), []byte(auth), 0o600))
}

func (e *testEnv) request(mods ...func(*core.Request)) core.Request {
	return fixtures.RequestWith(func(r *core.Request) {
		r.RunID, r.SessionID, r.Workspace = "", "", e.workspace
		for _, mod := range mods {
			mod(r)
		}
	})
}

func (e *testEnv) run(t *testing.T, ctx context.Context, mods ...func(*core.Request)) (core.Result, error) {
	t.Helper()
	sink := stream.NewSink(&e.stream)

	return e.h.Run(ctx, e.request(mods...), func(ev core.Event) {
		e.events = append(e.events, ev)
		sink.Emit(ev)
	})
}

func TestRun_Golden(t *testing.T) {
	for _, name := range []string{"simple", "parallel"} {
		t.Run(name, func(t *testing.T) {
			e := newTestEnv(t, name+".jsonl")

			result, err := e.run(t, context.Background())

			require.NoError(t, err)
			assert.Equal(t, core.StatusOK, result.Status)
			assertGolden(t, name+".stream.jsonl", normalizeStream(t, e.stream.Bytes()))
		})
	}
}

func TestRun_Records(t *testing.T) {
	e := newTestEnv(t, "simple.jsonl")

	result, err := e.run(t, context.Background())

	require.NoError(t, err)
	runDir := e.h.RunDir(result.Request.RunID)
	t.Run("events.jsonl is the runner output byte for byte", func(t *testing.T) {
		saved, err := os.ReadFile(filepath.Join(runDir, harness.EventsFile))
		require.NoError(t, err)
		assert.Equal(t, fixtures.RunnerOutput("simple.jsonl"), saved)
	})
	t.Run("the runner receives the request on stdin", func(t *testing.T) {
		stdin, err := os.ReadFile(filepath.Join(e.capture, "stdin.json"))
		require.NoError(t, err)
		saved, err := os.ReadFile(filepath.Join(runDir, harness.RequestFile))
		require.NoError(t, err)
		assert.JSONEq(t, string(saved), string(stdin))
		assert.JSONEq(t, `{"prompt":"Summarize this project.","thinking_level":"high","model":"gpt-6-sol","session_id":"`+result.Request.SessionID+`"}`, string(stdin))
	})
	t.Run("provider, model, and base URL are pinned", func(t *testing.T) {
		env, err := os.ReadFile(filepath.Join(e.capture, "env.txt"))
		require.NoError(t, err)
		assert.Contains(t, string(env), "UNREAL_HARNESS_LLM_PROVIDER=openai-codex\n")
		assert.Contains(t, string(env), "UNREAL_HARNESS_LLM_MODEL=gpt-6-sol\n")
		assert.Contains(t, string(env), "UNREAL_HARNESS_LLM_BASE_URL=\n", "an empty pin stops a workspace .env from setting it")
	})
	t.Run("summary.json matches run_finished", func(t *testing.T) {
		saved, err := os.ReadFile(filepath.Join(runDir, harness.SummaryFile))
		require.NoError(t, err)
		finished, ok := e.events[len(e.events)-1].(core.RunFinished)
		require.True(t, ok)
		want, err := json.Marshal(stream.SummaryToDTO(finished.Result))
		require.NoError(t, err)
		assert.JSONEq(t, string(want), string(saved))
	})
	t.Run("Load recomputes the same statistics", func(t *testing.T) {
		loaded, err := harness.Load(runDir)
		require.NoError(t, err)
		assert.Equal(t, result.Stats, loaded.Stats)
		assert.Equal(t, result.Answer, loaded.Answer)
		assert.Equal(t, result.Request.RunID, loaded.Request.RunID)
	})
}

func TestRun_RunnerFailure(t *testing.T) {
	e := newTestEnv(t, "error.jsonl")
	t.Setenv("FAKERUNNER_EXIT", "1")

	result, err := e.run(t, context.Background())

	require.NoError(t, err)
	assert.Equal(t, core.StatusFailed, result.Status)
	assert.Equal(t, 1, result.RunnerExitCode)
	assert.Equal(t, []string{"model must be set in the request or UNREAL_HARNESS_LLM_MODEL"}, result.Stats.Errors)
}

func TestRun_StopsHungRuns(t *testing.T) {
	tests := map[string]struct {
		timeout time.Duration
		cancel  time.Duration
		want    core.Status
	}{
		"timeout":   {timeout: 500 * time.Millisecond, want: core.StatusTimeout},
		"interrupt": {cancel: 500 * time.Millisecond, want: core.StatusInterrupted},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			e := newTestEnv(t, "timeout.jsonl")
			t.Setenv("FAKERUNNER_HANG", "1")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancel > 0 {
				time.AfterFunc(tc.cancel, cancel)
			}

			result, err := e.run(t, ctx, func(r *core.Request) { r.Timeout = tc.timeout })

			require.NoError(t, err)
			assert.Equal(t, tc.want, result.Status)
			assert.Equal(t, 1, result.Stats.ToolCalls)
			assert.Positive(t, result.Stats.ToolBusyTime, "the unfinished tool counts until the kill")
			assertAllDead(t, filepath.Join(e.capture, "hung.pids"))
			assert.IsType(t, core.RunFinished{}, e.events[len(e.events)-1])
		})
	}
}

func TestRun_KillsOrphanedTools(t *testing.T) {
	e := newTestEnv(t, "simple.jsonl")
	t.Setenv("FAKERUNNER_HANG", "orphan")

	result, err := e.run(t, context.Background())

	require.NoError(t, err)
	assert.Equal(t, core.StatusOK, result.Status)
	assertAllDead(t, filepath.Join(e.capture, "hung.pids"))
}

func TestRun_Preflight(t *testing.T) {
	t.Run("a risky .env blocks the run before the runner starts", func(t *testing.T) {
		e := newTestEnv(t, "simple.jsonl")
		require.NoError(t, os.WriteFile(filepath.Join(e.workspace, ".env"), []byte("UNREAL_HARNESS_LLM_BASE_URL=http://evil\n"), 0o600))

		_, err := e.run(t, context.Background())

		require.ErrorIs(t, err, harness.ErrPreflightBlocked)
		assert.ErrorContains(t, err, "UNREAL_HARNESS_LLM_BASE_URL")
		assert.Empty(t, e.events)
		assert.NoDirExists(t, filepath.Join(e.stateDir, "runs"))
	})

	t.Run("allow-dotenv turns it into a warning event", func(t *testing.T) {
		e := newTestEnv(t, "simple.jsonl")
		require.NoError(t, os.WriteFile(filepath.Join(e.workspace, ".env"), []byte("HTTPS_PROXY=x\n"), 0o600))

		result, err := e.run(t, context.Background(), func(r *core.Request) { r.AllowDotenv = true })

		require.NoError(t, err)
		assert.Equal(t, core.StatusOK, result.Status)
		warning, ok := e.events[1].(core.PreflightWarning)
		require.True(t, ok)
		assert.Equal(t, core.FindingDotenvRisky, warning.Code)
		assert.Len(t, result.Stats.Warnings, 1)
	})
}

func TestHistory(t *testing.T) {
	e := newTestEnv(t, "simple.jsonl")
	first, err := e.run(t, context.Background())
	require.NoError(t, err)
	t.Setenv("FAKERUNNER_FIXTURE", fixtures.Path("parallel.jsonl"))
	second, err := e.run(t, context.Background())
	require.NoError(t, err)

	runs, err := e.h.History()

	require.NoError(t, err)
	require.Len(t, runs, 2)
	assert.Equal(t, second.Request.RunID, runs[0].Request.RunID, "newest first")
	assert.Equal(t, first.Request.RunID, runs[1].Request.RunID)
	assert.Equal(t, "A; B", runs[0].Answer)
}

// normalizeStream replaces the values that differ between runs.
func normalizeStream(t *testing.T, data []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	for line := range strings.Lines(string(data)) {
		var ev map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &ev))
		switch ev["type"] {
		case "run_started", "preflight_warning", "run_finished":
			ev["at"] = "<now>"
		}
		for _, key := range []string{"run_id", "session_id", "workspace"} {
			if _, ok := ev[key]; ok {
				ev[key] = "<" + key + ">"
			}
		}
		if summary, ok := ev["summary"].(map[string]any); ok {
			for _, key := range []string{"run_id", "session_id", "workspace", "started_at", "wall_ms"} {
				summary[key] = "<" + key + ">"
			}
		}
		enc := json.NewEncoder(&out)
		enc.SetEscapeHTML(false)
		require.NoError(t, enc.Encode(ev))
	}

	return out.Bytes()
}

func assertGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := fixtures.GoldenPath(name)
	if *update {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, got, 0o600))
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err, "run go test ./harness -update to create golden files")
	assert.Equal(t, string(want), string(got))
}

func assertAllDead(t *testing.T, pidFile string) {
	t.Helper()
	data, err := os.ReadFile(pidFile)
	require.NoError(t, err)
	for field := range strings.FieldsSeq(string(data)) {
		pid, err := strconv.Atoi(field)
		require.NoError(t, err)
		assert.Eventually(t, func() bool { return syscall.Kill(pid, 0) != nil }, 2*time.Second, 20*time.Millisecond,
			"process %d should be dead", pid)
	}
}
