# uagent architecture

uagent follows a domain, adapter, and composition-root layout. These rules apply to every change.

- `domain/` is pure: entities, events, `RunService`, `StatsCollector`, and ports (`Runner`, `Preflight`, `RunStore`, `EventSink`). Standard library and `uuid` only; no `os`, `os/exec`, `encoding/json`, `slog`, or struct tags.
- Adapters implement ports or transform domain types for a boundary: `runner/` (subprocess, JSONL decoding), `preflight/`, `runstore/`, `statedir/`, `stream/` (versioned JSONL contract), `render/` (terminal output). Wire DTOs with `json` tags live in each adapter's `serialization.go`.
- `cmd/uagent/` is the composition root: flags, wiring, logger, exit codes. `main` owns the only `os.Exit`.
- Services: unexported struct, exported interface, constructor returns the interface, `ctx` first.
- Errors: wrap with `fmt.Errorf("failed to <action>: %w", err)`. Sentinels only when a caller branches with `errors.Is` (`domain.ErrPreflightBlocked`). Findings and run statuses are results, not errors.
- Logging: `slog` logger instances with `LogAttrs` and snake_case keys, to stderr. Stdout is reserved for the answer, the summary JSON, or the stream.

## Change rules

- A change to event or summary fields must update `stream/README.md`. Removing or renaming a field bumps `stream.SchemaVersion`; rs-uagent-tui depends on it.
- Tests: black-box `_test` packages, harness with mockery mocks (`go generate ./...`), fixtures in `testing/fixtures`. Real runner output only, with local paths removed.
- Before committing: `go test ./...`, `golangci-lint run ./...`, and `memoria check`. After changing code, follow `docs/documentation/memoria.md` to review the affected READMEs and commit `memoria.lock`.
