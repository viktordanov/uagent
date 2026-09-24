<!-- memoria:section id="overview" files="go.mod harness/harness.go" -->
# uagent

<p>
  <a href="https://github.com/viktordanov/uagent/actions/workflows/ci.yml"><img src="https://github.com/viktordanov/uagent/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/viktordanov/uagent/actions/workflows/memoria.yml"><img src="https://github.com/viktordanov/uagent/actions/workflows/memoria.yml/badge.svg" alt="Docs checked by Memoria"></a>
  <a href="https://github.com/viktordanov/uagent/releases/latest"><img src="https://img.shields.io/github/v/release/viktordanov/uagent" alt="Release"></a>
  <a href="https://pkg.go.dev/github.com/viktordanov/uagent"><img src="https://pkg.go.dev/badge/github.com/viktordanov/uagent.svg" alt="Go reference"></a>
</p>

<!-- memoria:export id="summary" -->
uagent is a wrapper around unreal-agent-runner that adds safety guards and everyday ergonomics while staying as close to the original runner as possible.
<!-- /memoria:export -->

[uah](https://github.com/viktordanov/uagent-harness), a Codex-style TUI, is built on it.

> [!NOTE]
> The docs are kept in sync with the code by [Memoria](https://github.com/viktordanov/rs-memoria): CI fails when code changes and its README hasn't been reviewed.

1. [Why a wrapper](#why-a-wrapper)
2. [Install](#install)
3. [Run a task](#run-a-task)
4. [CLI reference](#cli-reference)
5. [What uagent adds](#what-uagent-adds)
6. [Use it from Go](#use-it-from-go)
7. [Development](#development)
8. [License](#license)

## Why a wrapper

[unreal-agent-runner](https://github.com/unreallabsai/unreal-agent) is the headless runner of Unreal Agent, a Go agent harness whose tool calls run in the background while the model keeps working.
It runs one task from a JSON request, writes session items as JSONL, and exits, much like `codex exec` or `claude -p`.

The runner is capable but raw. It stores sessions inside the workspace, loads the workspace `.env`, has no overall timeout, and prints session items rather than an answer.
uagent keeps the runner itself unchanged: the same request fields reach it on stdin, its output is saved byte for byte, and nothing is added to the prompt.
What uagent adds sits around the runner: guards before and during the run, readable progress, a plain answer on stdout, and a record of every run.
<!-- /memoria:section -->

<!-- memoria:section id="usage" files="go.mod cmd/uagent/app.go cmd/uagent/main.go harness/preflight.go" -->
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

The prompt comes from the arguments, or from stdin when there are none or the only argument is `-`.
Flags can come before or after the prompt.

## CLI reference

`uagent [flags] <prompt>` runs one task. `uagent stats [--json] <run-dir|events.jsonl>` recomputes the summary of a saved run.

### Choose a backend and model

| Flag | Default | Environment | Meaning |
| --- | --- | --- | --- |
| `--provider` | `openai-codex` | `UNREAL_HARNESS_LLM_PROVIDER` | How the runner authenticates and which endpoint it calls by default |
| `-m`, `--model` | `gpt-6-sol` for `openai-codex`, `gpt-6-astra` for `openai` | `UNREAL_HARNESS_LLM_MODEL` | The provider's model ID, passed through unchanged |
| `-e`, `--effort` | `high` | | Thinking level: `low`, `medium`, `high`, `xhigh`, or `max` |
| `--base-url` | the provider's endpoint | `UNREAL_HARNESS_LLM_BASE_URL` | Send requests to another server |
| `--max-attempts` | the runner's default (5) | | Retries for failed model requests; 1 disables retries |
| `--system-prompt` | the runner's prompt | | Replace the runner's system prompt |
| `--disallow` | | | A tool the model may not use (`Bash`, `ViewImage`, or `SkillUse`); repeat for more |

Every provider speaks the OpenAI Responses API and posts to `<base URL>/responses`. The provider decides the credentials and the default endpoint:

| Provider | Credentials | Default endpoint | Default model |
| --- | --- | --- | --- |
| `openai-codex` | `codex login` (`~/.codex/auth.json` or `$CODEX_HOME/auth.json`), or `OPENAI_CODEX_ACCESS_TOKEN` | `https://chatgpt.com/backend-api/codex` | none; uagent uses `gpt-6-sol` |
| `openai` | `OPENAI_API_KEY` | `https://api.openai.com/v1` | `gpt-6-astra` |
| `openrouter` | `OPENROUTER_API_KEY` | `https://openrouter.ai/api/v1` | none; pass `--model` |
| `fireworks` | `FIREWORKS_API_KEY` | `https://api.fireworks.ai/inference/v1` | none; pass `--model` |
| `ollama` | none | `http://localhost:11434/v1` | none; pass `--model` |

`UNREAL_HARNESS_LLM_API_KEY` works for every keyed provider. uagent checks credentials and the model before starting, so a missing key or model fails immediately with exit code 2.

```sh
# Codex subscription (the default backend)
uagent --provider openai-codex --model gpt-6-sol --effort high "..."

# OpenAI API key
uagent --provider openai --model gpt-6-astra --effort medium "..."

# OpenRouter: any model ID it lists
uagent --provider openrouter --model <vendor>/<model> --effort high "..."

# Fireworks
uagent --provider fireworks --model accounts/fireworks/models/<model> --effort medium "..."

# Local Ollama, or Ollama on another machine
uagent --provider ollama --model <model> --effort low "..."
uagent --provider ollama --model <model> --effort low --base-url http://gpu-box:11434/v1 "..."

# Any server that speaks the Responses API
uagent --provider openai --model <model> --effort medium --base-url http://localhost:8000/v1 "..."
```

The `openai` provider always needs `OPENAI_API_KEY` or `UNREAL_HARNESS_LLM_API_KEY`; for a local server that ignores keys, any value works.

To make a backend the default, export the variables, for example `export UNREAL_HARNESS_LLM_PROVIDER=openrouter UNREAL_HARNESS_LLM_MODEL=<vendor>/<model>`.
Flags win over the environment. uagent always passes the provider, model, and base URL to the runner explicitly, so a workspace `.env` cannot change them.

### Guards and limits

| Flag | Default | Environment | Meaning |
| --- | --- | --- | --- |
| `-C`, `--workspace` | `.` | | The agent's workspace and Bash working directory |
| `-t`, `--timeout` | `30m` | | Stop the run and all its tools after this long; `0` disables |
| `--max-disk` | `5G` | | Stop the run when tool output passes this size (`500M`, `2G`); `0` disables |
| `--allow-dotenv` | off | | Run even when the workspace `.env` sets risky variables, with a warning |
| `--state-dir` | `~/.local/state/unreal-agent` | `UAGENT_STATE_DIR` | Sessions, logs, and run records; must be outside the workspace |
| `--runner` | `~/.local/bin`, then `PATH` | `UAGENT_RUNNER` | The unreal-agent-runner executable |
| `--session` | a new UUID | | Create or resume a named runner session |

### Output

| Flag | Stdout | Stderr |
| --- | --- | --- |
| none | the final answer | progress, then a summary |
| `-q`, `--quiet` | the final answer | the summary |
| `--json` | the summary with the answer, as JSON | progress, then the summary |
| `--stream` | versioned JSONL events | diagnostics only |

`--verbose` adds the model's reasoning summaries to the progress, and `--log-level debug|info|warn|error` (default `warn`) controls diagnostic logs on stderr.
`--json` and `--stream` cannot be combined. `-v` prints the version.

| Exit code | Meaning |
| --- | --- |
| 0 | The run finished and the runner reported no error |
| 1 | The run failed (the runner exited non-zero, reported an error, or the model failed), or uagent hit an unexpected error |
| 2 | Invalid usage, a preflight check blocked the run, or another run holds the session |
| 3 | The disk limit stopped the run |
| 124 | The timeout stopped the run |
| 130 | The run was interrupted |
<!-- /memoria:section -->

<!-- memoria:section id="contract" files="harness/preflight.go harness/process.go harness/lock.go harness/state.go" -->
## What uagent adds

### Safety guards

| Runner behavior | Guard |
| --- | --- |
| Sessions stored in the workspace can be read back by the agent's own background searches and grow without limit (unreal-agent issue #3). | Sessions, logs, and run records live in `~/.local/state/unreal-agent`. A state directory inside the workspace is refused. `--max-disk` (default 5G) stops a run whose tool output grows past the limit. |
| The workspace `.env` is loaded and can redirect the model endpoint or credentials (issue #5). | A `.env` that sets `UNREAL_HARNESS_*`, `OPENAI_CODEX_*`, `CODEX_HOME`, or `*_PROXY` blocks the run unless `--allow-dotenv` is given. Provider, model, and base URL are always set explicitly for the runner, so a `.env` cannot override them. |
| No overall time limit, and background tools can outlive a run. | `--timeout` (default 30m) and Ctrl+C stop the runner gracefully: SIGINT first, so it records its state, then SIGTERM and SIGKILL if it does not stop within 5 s. Tools and processes still running after the runner exits, for any reason, are killed, and tools left behind by a run that was itself killed are killed when its session next starts. |
| Session files have no lock, so two runs on one session corrupt it. | Each run holds a lock on its session (`sessions/<id>.lock`). A second run on the same `--session` fails at once with exit code 2. |
| Codex tokens are never refreshed. | An expired token stops the run before it starts; a token that expires within an hour produces a warning. |

### Ergonomics

- A plain answer on stdout, with progress and a summary on stderr, so uagent composes with pipes and scripts.
- A prompt from an argument or stdin, sensible defaults, and flags that validate their values.
- A record of every run in `~/.local/state/unreal-agent/runs/<run-id>/`: `request.json`, the raw runner output in `events.jsonl`, `stderr.log`, and `summary.json`.
- Statistics for comparing the runner with other agents: wall time, model time, tool busy time, tool time that overlapped model time, turns, tool calls and failures, parallelism, and tokens.
<!-- /memoria:section -->

<!-- memoria:section id="library" files="core/run.go core/event.go core/stats.go core/finding.go harness/harness.go harness/backend.go harness/run.go harness/lock.go harness/state.go harness/decode.go" -->
## Use it from Go

The CLI is a thin layer over two packages, and a TUI or another tool can use them directly:

- `core` is the pure model: `Request`, `Result`, the run events, `StatsCollector`, and the rules for findings and outcomes. It does no I/O.
- `harness` runs the runner with every guard. `New(Config)` returns a `Harness`:
  - `Run(ctx, request, sink)` streams events to `sink` and returns the `Result`. `Start` does the same but returns a `Run` handle at once, with `Wait`, `Interrupt` (graceful), `Kill`, and `Done`.
  - A request carries either a `Prompt` or `Messages`, each with an ID. The runner echoes every message as a `UserMessage` event with the same ID, which confirms delivery.
  - `Preflight` checks a request without running it. `LockSession` is the per-session lock every run takes.
  - `Config.Backend` swaps how the agent runs. The default, `RunnerBackend`, spawns the runner (its `Env` adds variables to the runner's environment, such as `SHELL`); another backend can run the runner's packages in process and keep every guard, the lock, and the run records. `Run.Process` returns the backend's handle.
  - `Runs` lists every run record, including runs in progress; `History` lists finished runs; `LoadRequest`, `LoadEvents`, and `Load` reopen one.

```go
h := harness.New(harness.Config{RunnerPath: runner, StateDir: harness.DefaultStateDir()})
result, err := h.Run(ctx, core.Request{Prompt: "Summarize this project.", Provider: "openai-codex",
	Model: "gpt-6-sol", Effort: "high", Workspace: dir}, func(e core.Event) { /* update the UI */ })
```

`Run` and `Start` send `RunStarted` first and `RunFinished` last for every run that starts, and they are safe to call concurrently for different sessions.
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

<!-- memoria:section id="license" files="LICENSE NOTICE" -->
## License

uagent is licensed under the [Apache License 2.0](LICENSE); keep the [NOTICE](NOTICE) when you redistribute it. It runs [unreal-agent-runner](https://github.com/unreallabsai/unreal-agent) (MIT) as a separate program and includes none of its code.
<!-- /memoria:section -->
