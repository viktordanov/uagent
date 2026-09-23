# uagent architecture

uagent is a CLI with a small pure core and well-organized infrastructure around it.

| Package | Role |
| --- | --- |
| `core` | Pure model and rules: request, result, events, statistics, finding triage, and outcome classification. Standard library only, no I/O. |
| `harness` | Infrastructure, one concern per file: `harness.go` (the `Run` flow and public API), `process.go` (subprocess and process groups), `decode.go` and `wire.go` (runner JSONL), `preflight.go`, and `state.go` (state directory and run records). |
| `stream` | The versioned JSONL encoding of core events and summaries, for `--stream` and `summary.json`. |
| `cmd/uagent` | The CLI: flags, terminal rendering, and exit codes. |
| `testing` | Fixtures, the fake runner, and golden files. |

## Rules

- `core` never imports `os`, `os/exec`, `encoding/json`, or `log/slog`, and its structs carry no tags. A decision that needs no I/O belongs in `core`.
- Wire formats stay at the edge: runner JSON in `harness/wire.go`, stream JSON in `stream`.
- Wrap errors with `fmt.Errorf("failed to <action>: %w", err)`. Add a sentinel only when a caller branches on it; `harness.ErrPreflightBlocked` is the one today. Findings and run statuses are results, not errors.
- Log with `slog` logger instances and `LogAttrs`, snake_case keys, to stderr. Stdout belongs to the answer, the summary JSON, or the stream.
- `main` owns the only `os.Exit`.
- Test through real code paths: the fake runner and captured fixtures instead of mocks.
- Changing an event or summary field updates `stream/README.md` and the golden files. Removing or renaming a field bumps `stream.SchemaVersion`.
