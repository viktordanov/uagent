<!-- memoria:section id="overview" files="go.mod" -->
# uagent

<!-- memoria:export id="summary" -->
uagent runs one unreal-agent-runner task with safety guards, shows readable progress, prints the final answer, and records statistics for each run so the runner can be compared with codex exec and claude -p.
<!-- /memoria:export -->

1. [Install](#install)
2. [Run a task](#run-a-task)
3. [Guards](#guards)
4. [Run records](#run-records)
5. [Packages](#packages)
6. [Development](#development)

[unreal-agent-runner](https://github.com/unreallabsai/unreal-agent) is the headless runner of Unreal Agent, a Go agent harness whose tool calls run asynchronously.
It runs one task, writes session items as JSONL, and exits. uagent wraps it with the guards the runner lacks and turns its output into progress, an answer, and comparable statistics.
<!-- /memoria:section -->

<!-- memoria:section id="usage" files="go.mod" -->
## Install

```sh
GOBIN="$HOME/.local/bin" go install github.com/unreallabsai/unreal-agent/cmd/unreal-agent-runner@latest
GOBIN="$HOME/.local/bin" go install github.com/viktordanov/uagent/cmd/uagent@latest
codex login   # for the default openai-codex provider
```

## Run a task

```sh
uagent -e xhigh "Summarize this project."
uagent -e medium -t 20m -C ~/code/proj "Fix the failing test in pkg/foo"
uagent --json "..." | jq .stats.tokens            # answer and statistics as JSON
uagent --stream "..."                              # JSONL events for another program
uagent stats ~/.local/state/unreal-agent/runs/<run-id>
```

The default provider is `openai-codex` with `gpt-6-sol`. `uagent --help` lists every flag.
Progress and the summary go to stderr; stdout carries only the answer, the summary JSON, or the event stream.
<!-- /memoria:section -->

<!-- memoria:section id="contract" files="go.mod" -->
## Guards

| Risk | What uagent does |
| --- | --- |
| Session output inside the workspace grows without limit (unreal-agent issue #3) | Sessions and logs live in `~/.local/state/unreal-agent`. A state directory inside the workspace is refused, and `--max-disk` (default 5G) kills a run whose tool output grows past the limit. |
| A workspace `.env` redirects the endpoint or credentials (issue #5) | A `.env` that sets `UNREAL_HARNESS_*`, `OPENAI_CODEX_*`, `CODEX_HOME`, or `*_PROXY` blocks the run unless `--allow-dotenv` is given. Provider, model, and base URL are always pinned for the runner. |
| No per-command timeout | `--timeout` (default 30m) stops the runner and every background tool process group: SIGTERM, then SIGKILL. |
| The runner does not refresh Codex tokens | An expired token blocks the run before it starts; a token that expires within an hour produces a warning. |

Exit codes: 0 ok, 1 failed, 2 usage or preflight, 3 disk limit, 124 timeout, 130 interrupted.

## Run records

Each run gets `~/.local/state/unreal-agent/runs/<run-id>/` with `request.json`, the raw runner output in `events.jsonl`, `stderr.log`, and `summary.json`.
The summary records wall time, model time, tool busy time, tool time that overlapped model time, turns, tool calls by name, failed calls, parallelism, and tokens.
<!-- /memoria:section -->

## Packages

`domain` is the pure core. The other packages are adapters for its ports, and `cmd/uagent` wires them together.

### [cmd/uagent](cmd/uagent/README.md)
<!-- memoria:import src="cmd/uagent/README.md#summary" -->
The uagent command is the composition root: it defines the CLI with urfave/cli v3, wires the adapters into the domain run service, chooses the output mode, and maps outcomes to exit codes.
<!-- /memoria:import -->

### [domain](domain/README.md)
<!-- memoria:import src="domain/README.md#summary" -->
The domain package holds uagent's core model: run requests and results, normalized run events, statistics, and the run service with its ports. It depends only on the Go standard library and uuid.
<!-- /memoria:import -->

### [runner](runner/README.md)
<!-- memoria:import src="runner/README.md#summary" -->
The runner package drives unreal-agent-runner as a subprocess, turns its JSONL output into domain events, and stops the runner and its background tools on timeout, interrupt, or runaway disk use.
<!-- /memoria:import -->

### [preflight](preflight/README.md)
<!-- memoria:import src="preflight/README.md#summary" -->
The preflight package checks the workspace, the state directory, the workspace .env file, and provider credentials before a run, and reports each problem as a domain finding.
<!-- /memoria:import -->

### [stream](stream/README.md)
<!-- memoria:import src="stream/README.md#summary" -->
The stream package writes run events as versioned JSONL for `uagent --stream`. It is the contract for programs that drive uagent, such as rs-uagent-tui.
<!-- /memoria:import -->

### [render](render/README.md)
<!-- memoria:import src="render/README.md#summary" -->
The render package formats run events and summaries for people reading a terminal: live progress lines and the end-of-run summary block.
<!-- /memoria:import -->

### [runstore](runstore/README.md)
<!-- memoria:import src="runstore/README.md#summary" -->
The runstore package saves each finished run as summary.json in its run directory and loads saved summaries for uagent stats.
<!-- /memoria:import -->

### [statedir](statedir/README.md)
<!-- memoria:import src="statedir/README.md#summary" -->
The statedir package defines where uagent keeps runner sessions, logs, and per-run records, always outside the agent workspace.
<!-- /memoria:import -->

### [testing](testing/README.md)
<!-- memoria:import src="testing/README.md#summary" -->
The testing directory holds deterministic fixtures (captured runner output and event builders) and the generated mocks used by the unit tests.
<!-- /memoria:import -->

### [docs](docs/README.md)
<!-- memoria:import src="docs/README.md#summary" -->
Architecture rules, writing guidance, section conventions, and the Memoria review procedure for uagent.
<!-- /memoria:import -->

<!-- memoria:section id="development" files="generate.go .golangci.yml .mockery.yaml .github/workflows/ci.yml .github/workflows/memoria.yml" -->
## Development

```sh
go generate ./...          # mockery v3 mocks into testing/mocks (not committed)
go test ./...              # add -short to skip subprocess tests
golangci-lint run ./...    # aicore's linter set plus sloglint, formatted with gofumpt
memoria check              # documentation freshness
```

CI runs the same steps on every pull request and push to `main`: mock generation, build, race-enabled tests, and golangci-lint in [ci.yml](.github/workflows/ci.yml), and `memoria check` in the Memoria-managed [memoria.yml](.github/workflows/memoria.yml).

The [architecture rules](docs/documentation/architecture.md) describe the domain, adapter, and composition-root layout.
Every README is kept in step with its code by Memoria. The [documentation guide](docs/README.md) explains the conventions and the review procedure.
<!-- /memoria:section -->
