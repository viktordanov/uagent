<!-- memoria:section id="overview" files="runner.go" -->
# Runner

<!-- memoria:export id="summary" -->
The runner package drives unreal-agent-runner as a subprocess, turns its JSONL output into domain events, and stops the runner and its background tools on timeout, interrupt, or runaway disk use.
<!-- /memoria:export -->

1. [Set it up](#set-it-up)
2. [Run a request](#run-a-request)
3. [Decode runner output](#decode-runner-output)
4. [Stop a run](#stop-a-run)
5. [Runner facts](#runner-facts)
6. [Tests](#tests)
<!-- /memoria:section -->

<!-- memoria:section id="setup" files="runner.go" -->
## Set it up

`Resolve(explicit)` finds the binary: the explicit path, then `~/.local/bin/unreal-agent-runner`, then `PATH`.
`New(Config)` returns a `domain.Runner`:

| Field | Meaning |
| --- | --- |
| `Bin` | Path to unreal-agent-runner |
| `Layout` | The state directory layout (see `statedir`) |
| `MaxDisk` | Kill the run when the session's tool output exceeds this many bytes; 0 disables the check |
| `KillGrace` | Wait between SIGTERM and SIGKILL; default 5 seconds |
| `Logger` | Diagnostic logger; default `slog.Default()` |
<!-- /memoria:section -->

<!-- memoria:section id="usage" files="runner.go serialization.go" -->
## Run a request

`Run` creates the run directory and writes `request.json`, the exact JSON the runner receives on stdin.
It starts the runner in its own process group with `-workspace`, `-session-directory`, and `-log-directory`, where both directories are under the state root.
Stdout is copied byte for byte to `events.jsonl` and decoded line by line. Stderr goes to `stderr.log`.

The child environment is the parent's, with `UNREAL_HARNESS_LLM_PROVIDER`, `UNREAL_HARNESS_LLM_MODEL`, and `UNREAL_HARNESS_LLM_BASE_URL` always set, even when empty.
The runner loads the workspace `.env` only for variables that are not already set, so this pinning stops a workspace `.env` from redirecting the endpoint.
<!-- /memoria:section -->

<!-- memoria:section id="decoding" files="decode.go serialization.go" -->
## Decode runner output

`Decoder` keeps state across lines and emits normalized events:

- A `turn` item becomes `TurnStarted`, numbered from 1.
- A `model_response` item becomes `ModelResponded` (with the time since the turn started), then one event per output item: `ToolCalled`, `AssistantMessage` (final when `Phase` is `final_answer`), or `ReasoningSummary`.
- A `tool_call_status` item becomes `ToolStarted` the first time an operation appears, and `ToolFinished` the first time it reaches `completed`, `failed`, or `canceled`. A shell operation is OK only when it completed with exit code 0. A call-level `Status.Error` becomes a failed `ToolFinished` without an operation ID.
- `{"type":"error"}` lines and lines that are not JSON become `RunnerError`.

`ReadEvents` decodes a saved `events.jsonl`; `uagent stats` uses it.
<!-- /memoria:section -->

<!-- memoria:section id="lifecycle" files="runner.go" -->
## Stop a run

When the context ends, or every five seconds the session's operations directory is larger than `MaxDisk`, the runner is stopped:

1. SIGTERM goes to the runner's process group and to every background tool process group that the session file still records as running.
2. After `KillGrace`, SIGKILL goes to the same groups if the runner has not exited.
3. SIGKILL goes to any tool group that is still recorded as running after the runner exited.

`RunnerExit.Termination` reports `exited`, `canceled`, or `disk_limit`. The output reader is drained for up to two seconds after the runner exits.
<!-- /memoria:section -->

<!-- memoria:section id="facts" files="serialization.go" -->
## Runner facts

Checked against unreal-agent-runner v0.1.1 source and live runs:

- The request is JSON on stdin with `prompt`, `thinking_level`, `model`, `session_id`, `system_prompt`, `disallowed_tools`, and `max_attempts`. Unknown fields are rejected.
- Stdout carries session items `{Sequence, RecordedAt, Kind, Data}` with kinds `input`, `turn`, `model_response`, and `tool_call_status`. A failure is `{"type":"error","message":...}` with exit code 1.
- The default provider is `openai`. `openai-codex` has no default model and reads `~/.codex/auth.json` without refreshing it.
- Background tool process group IDs appear in the session file's `operation` records, not reliably on stdout.
- The tools are `Bash`, `ViewImage`, and `SkillUse`.
<!-- /memoria:section -->

<!-- memoria:section id="testing" files="decode_test.go runner_test.go" -->
## Tests

`decode_test.go` decodes the captured runs in `testing/fixtures/runner/` and checks the resulting events and stats.
`runner_test.go` replaces the runner with a shell script. It checks the stdin request, the pinned environment, the copied events, exit codes, and that cancellation kills a background child process. These tests are skipped with `go test -short`.
<!-- /memoria:section -->
