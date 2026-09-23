<!-- memoria:section id="overview" files="go.mod harness/harness.go" -->
# uagent

<!-- memoria:export id="summary" -->
uagent is a wrapper around unreal-agent-runner that adds safety guards and everyday ergonomics while staying as close to the original runner as possible.
<!-- /memoria:export -->

1. [Why a wrapper](#why-a-wrapper)
2. [Install](#install)
3. [Run a task](#run-a-task)
4. [What uagent adds](#what-uagent-adds)
5. [Use it from Go](#use-it-from-go)
6. [Development](#development)

## Why a wrapper

[unreal-agent-runner](https://github.com/unreallabsai/unreal-agent) is the headless runner of Unreal Agent, a Go agent harness whose tool calls run in the background while the model keeps working.
It runs one task from a JSON request, writes session items as JSONL, and exits, much like `codex exec` or `claude -p`.

The runner is capable but raw. It stores sessions inside the workspace, loads the workspace `.env`, has no overall timeout, and prints session items rather than an answer.
uagent keeps the runner itself unchanged: the same request fields reach it on stdin, its output is saved byte for byte, and nothing is added to the prompt.
What uagent adds sits around the runner: guards before and during the run, readable progress, a plain answer on stdout, and a record of every run.
<!-- /memoria:section -->

<!-- memoria:section id="usage" files="go.mod cmd/uagent/app.go" -->
## Install

```sh
GOBIN="$HOME/.local/bin" go install github.com/unreallabsai/unreal-agent/cmd/unreal-agent-runner@latest
GOBIN="$HOME/.local/bin" go install github.com/viktordanov/uagent/cmd/uagent@latest
codex login   # for the default openai-codex provider
```

## Run a task

```sh
uagent "Summarize this project."
uagent -e xhigh -t 20m -C ~/code/proj "Fix the failing test in pkg/foo"
echo "Explain the build" | uagent -q
uagent --json "..." | jq .stats.tokens     # answer and statistics as JSON
uagent --stream "..."                       # JSONL events for another program
uagent stats ~/.local/state/unreal-agent/runs/<run-id>
```

The default provider is `openai-codex` with `gpt-6-sol`. Flags can come before or after the prompt, and `uagent --help` lists them all.
`--provider`, `--model`, and `--base-url` also read the runner's own `UNREAL_HARNESS_LLM_*` variables.

| Output mode | Stdout | Stderr |
| --- | --- | --- |
| default | the final answer | progress, then a summary |
| `-q` | the final answer | the summary |
| `--json` | the summary with the answer, as JSON | progress, then the summary |
| `--stream` | versioned JSONL events | diagnostics only |

Exit codes: 0 ok, 1 failed, 2 usage or preflight, 3 disk limit, 124 timeout, 130 interrupted.
<!-- /memoria:section -->

<!-- memoria:section id="contract" files="harness/preflight.go harness/process.go harness/state.go" -->
## What uagent adds

### Safety guards

| Runner behavior | Guard |
| --- | --- |
| Sessions stored in the workspace can be read back by the agent's own background searches and grow without limit (unreal-agent issue #3). | Sessions, logs, and run records live in `~/.local/state/unreal-agent`. A state directory inside the workspace is refused. `--max-disk` (default 5G) stops a run whose tool output grows past the limit. |
| The workspace `.env` is loaded and can redirect the model endpoint or credentials (issue #5). | A `.env` that sets `UNREAL_HARNESS_*`, `OPENAI_CODEX_*`, `CODEX_HOME`, or `*_PROXY` blocks the run unless `--allow-dotenv` is given. Provider, model, and base URL are always set explicitly for the runner, so a `.env` cannot override them. |
| No overall time limit, and background tools can outlive a run. | `--timeout` (default 30m) stops the runner and every background tool process group, first with SIGTERM and then SIGKILL. Tools still running after the runner exits on its own are killed too. |
| Codex tokens are never refreshed. | An expired token stops the run before it starts; a token that expires within an hour produces a warning. |

### Ergonomics

- A plain answer on stdout, with progress and a summary on stderr, so uagent composes with pipes and scripts.
- A prompt from an argument or stdin, sensible defaults, and flags that validate their values.
- A record of every run in `~/.local/state/unreal-agent/runs/<run-id>/`: `request.json`, the raw runner output in `events.jsonl`, `stderr.log`, and `summary.json`.
- Statistics for comparing the runner with other agents: wall time, model time, tool busy time, tool time that overlapped model time, turns, tool calls and failures, parallelism, and tokens.
<!-- /memoria:section -->

<!-- memoria:section id="library" files="core/run.go core/event.go core/stats.go core/finding.go harness/harness.go harness/state.go harness/decode.go" -->
## Use it from Go

The CLI is a thin layer over two packages, and a TUI or another tool can use them directly:

- `core` is the pure model: `Request`, `Result`, the run events, `StatsCollector`, and the rules for findings and outcomes. It does no I/O.
- `harness` runs the runner with every guard. `New(Config)` returns a `Harness`; `Run(ctx, request, sink)` streams events to `sink` and returns the `Result`. `Preflight` checks a request without running it, `History` lists saved runs, and `Load` reopens one.

```go
h := harness.New(harness.Config{RunnerPath: runner, StateDir: harness.DefaultStateDir()})
result, err := h.Run(ctx, core.Request{Prompt: "Summarize this project.", Provider: "openai-codex",
	Model: "gpt-6-sol", Effort: "high", Workspace: dir}, func(e core.Event) { /* update the UI */ })
```

`Run` sends `RunStarted` first and `RunFinished` last for every run that starts, and it is safe to call concurrently.
Programs in other languages use the stream instead:

<!-- memoria:import src="stream/README.md#summary" -->
The stream package writes run events as versioned JSONL for `uagent --stream`. It is the contract for programs that drive uagent from outside Go.
<!-- /memoria:import -->
<!-- /memoria:section -->

<!-- memoria:section id="development" files=".golangci.yml .github/workflows/ci.yml .github/workflows/memoria.yml" -->
## Development

```sh
go test ./...              # unit, golden, and end-to-end tests; no model or tokens needed
golangci-lint run ./...    # the aicore linter set plus sloglint, formatted with gofumpt
memoria check              # documentation freshness
```

The tests replay real runner output through a fake runner:

<!-- memoria:import src="testing/README.md#summary" -->
Real captured runner output, a fake runner that replays it with its original timing, and golden files, so uagent is tested end to end without a model or tokens.
<!-- /memoria:import -->

CI runs the build, the race-enabled tests, and golangci-lint in [ci.yml](.github/workflows/ci.yml), and `memoria check` in the Memoria-managed [memoria.yml](.github/workflows/memoria.yml).
The [architecture notes](docs/documentation/architecture.md) explain the layout, and the [documentation guide](docs/README.md) explains how the READMEs are maintained.
<!-- /memoria:section -->
