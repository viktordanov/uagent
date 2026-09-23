<!-- memoria:section id="overview" files="main.go app.go" -->
# uagent command

<!-- memoria:export id="summary" -->
The uagent command is the composition root: it defines the CLI with urfave/cli v3, wires the adapters into the domain run service, chooses the output mode, and maps outcomes to exit codes.
<!-- /memoria:export -->

1. [Commands](#commands)
2. [Output modes](#output-modes)
3. [Exit codes](#exit-codes)
<!-- /memoria:section -->

<!-- memoria:section id="usage" files="app.go" -->
## Commands

`uagent [flags] <prompt>` runs one task. The prompt comes from stdin when it is omitted or `-`. Flags can appear before or after the prompt.
`uagent stats [--json] <run-dir|events.jsonl>` recomputes statistics from a saved `events.jsonl` and keeps the metadata from `summary.json` when it exists.

Flags that map to runner settings also read the runner's environment variables: `--provider` (`UNREAL_HARNESS_LLM_PROVIDER`), `--model` (`UNREAL_HARNESS_LLM_MODEL`), and `--base-url` (`UNREAL_HARNESS_LLM_BASE_URL`).
`--runner` reads `UAGENT_RUNNER` and `--state-dir` reads `UAGENT_STATE_DIR`. With `openai-codex` and no model, the model is `gpt-6-sol`.
`--effort`, `--provider`, `--max-disk`, and `--log-level` are validated when the flags are parsed.
`uagent --help` lists every flag.

## Output modes

| Mode | Stdout | Stderr |
| --- | --- | --- |
| default | final answer | progress, then the summary |
| `-q` | final answer | summary |
| `--json` | summary JSON with the answer | progress, then the summary |
| `--stream` | JSONL events (see `stream`) | diagnostics only |

`--json` and `--stream` cannot be combined. `--log-level` controls diagnostic logs on stderr (default `warn`); at `info`, each run logs one `run finished` event with its identity, status, duration, and counts.
<!-- /memoria:section -->

<!-- memoria:section id="contract" files="main.go" -->
## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | The run finished with status `ok` |
| 1 | The run failed, or uagent hit an unexpected error |
| 2 | Invalid usage, or preflight blocked the run |
| 3 | The run was killed by the disk limit |
| 124 | The run was killed by the timeout |
| 130 | The run was interrupted (SIGINT or SIGTERM) |

`main` owns the only `os.Exit`. Errors are printed once, prefixed with `uagent:`.
`--version` prints the version set at build time, or the module version for `go install` builds.
<!-- /memoria:section -->
