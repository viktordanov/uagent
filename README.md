# uagent

A thin wrapper around [`unreal-agent-runner`](https://github.com/unreallabsai/unreal-agent).
It runs one task, shows progress on stderr, prints the final answer on stdout,
and saves stats for each run so you can compare it with `codex exec` / `claude -p`.

Built with [urfave/cli v3](https://github.com/urfave/cli). Flags can go before or after the prompt; run `uagent --help` for all of them.

```sh
GOBIN="$HOME/.local/bin" go install .
uagent -e medium -t 20m -C ~/code/proj "Fix the failing test in pkg/foo"
uagent --json "..." | jq .tokens          # machine-readable summary
uagent stats ~/.local/state/unreal-agent/runs/<run>   # re-summarize a saved run
```

## Guards

| Risk | What uagent does |
|---|---|
| Session output inside the workspace grows without limit (issue #3) | Sessions and logs go to `~/.local/state/unreal-agent`. uagent refuses a `--state-dir` inside the workspace. `--max-disk` (default 5G) kills the run when tool output grows past the limit. |
| Workspace `.env` redirects the endpoint or credentials (issue #5) | uagent refuses to run if `.env` sets `UNREAL_HARNESS_*`, `OPENAI_CODEX_*`, `CODEX_HOME` or `*_PROXY`. It also sets provider, model and base URL for the runner, so `.env` cannot override them. |
| No per-command timeout | `--timeout` (default 30m) sends SIGTERM, then SIGKILL, to the runner's process group and to every background tool process group that is still running. |
| Expired Codex token (the runner does not refresh it) | uagent checks the `exp` claim in `auth.json` before the run starts. |

Exit codes: `0` ok · `1` failed · `2` usage or preflight error · `3` disk limit · `124` timeout · `130` interrupted.

## Run records

`<state-dir>/runs/<timestamp>-<session>/` contains `request.json`, `events.jsonl`
(the runner's raw stdout), `stderr.log` and `summary.json`.

Summary fields: wall time, model time, tool busy time, tool time that overlaps
model time (the runner's async benefit), turns, tool calls by name, failed
calls, max parallel tools, and tokens (input/cached/output/reasoning).

## Runner facts (v0.1.1, checked against the source)

- The request is JSON on stdin: `prompt`, `thinking_level`, `model`, `session_id`, `system_prompt`, `disallowed_tools`, `max_attempts`. Unknown fields are rejected.
- Stdout is session items: `{Sequence, RecordedAt, Kind, Data}`. `Kind` is `input`, `turn`, `model_response` or `tool_call_status`. On failure it is `{"type":"error","message":...}` with exit 1.
- The final answer is a `message` output item with `Phase: "final_answer"`.
- The default provider is `openai`, not `openai-codex`. `openai-codex` has no default model. uagent defaults to `openai-codex` + `gpt-6-sol`.
- The tools are `Bash`, `ViewImage` and `SkillUse`.
