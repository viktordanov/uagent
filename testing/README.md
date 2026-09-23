<!-- memoria:section id="overview" files="fixtures/fixtures.go fakerunner/main.go" -->
# Testing support

<!-- memoria:export id="summary" -->
Real captured runner output, a fake runner that replays it with its original timing, and golden files, so uagent is tested end to end without a model or tokens.
<!-- /memoria:export -->

1. [Fixtures](#fixtures)
2. [Fake runner](#fake-runner)
3. [Golden files](#golden-files)
4. [Where each layer is tested](#where-each-layer-is-tested)
<!-- /memoria:section -->

<!-- memoria:section id="fixtures" files="fixtures/fixtures.go fixtures/runner/simple.jsonl fixtures/runner/parallel.jsonl fixtures/runner/timeout.jsonl fixtures/runner/error.jsonl" -->
## Fixtures

`fixtures/runner/` holds unreal-agent-runner stdout captured from real runs, with local paths replaced by `/workspace` and `/state`:

| File | Run |
| --- | --- |
| `simple.jsonl` | Two parallel Bash calls, then the final answer `hello` |
| `parallel.jsonl` | Three Bash calls over four turns, then `A; B` |
| `timeout.jsonl` | One long Bash call that never finishes |
| `error.jsonl` | The runner's error line when no model is set |

`fixtures.RunnerOutput(name)` returns a capture's bytes and `fixtures.Path(name)` its file path.
The package also builds deterministic values: `T0`, `At(d)`, `Request()`, `RequestWith(fn)`, `Turn`, and `ToolRun`.
Add a fixture only from real runner output, and remove local paths and credentials before committing it.
<!-- /memoria:section -->

<!-- memoria:section id="fakerunner" files="fakerunner/main.go" -->
## Fake runner

`fakerunner` accepts the runner's flags and stdin request and replays a fixture. Environment variables control it:

| Variable | Effect |
| --- | --- |
| `FAKERUNNER_FIXTURE` | The capture to replay (required) |
| `FAKERUNNER_SPEED` | `0` writes everything at once (default), `1` keeps the original timing, `10` replays ten times faster |
| `FAKERUNNER_EXIT` | Exit code after the replay |
| `FAKERUNNER_HANG` | `1` starts background tools and waits to be killed; `orphan` starts them and exits |
| `FAKERUNNER_CAPTURE` | A directory that receives `stdin.json`, `env.txt`, and the pids of started tools |

The background tools are one child in the runner's process group and one in its own group, recorded in the session file the way the real runner records operations.
That lets the tests prove that timeouts, interrupts, and runner exits leave no process behind.

The fake runner also drives uagent by hand, for example while working on a TUI:

```sh
go build -o /tmp/fakerunner ./testing/fakerunner
FAKERUNNER_FIXTURE=$PWD/testing/fixtures/runner/parallel.jsonl FAKERUNNER_SPEED=1 \
  uagent --runner /tmp/fakerunner "any prompt"
```
<!-- /memoria:section -->

<!-- memoria:section id="golden" files="fixtures/golden/simple.stream.jsonl fixtures/golden/parallel.stream.jsonl" -->
## Golden files

`fixtures/golden/` holds the `--stream` output for each successful fixture, with run IDs, session IDs, paths, and wall-clock values replaced by placeholders.
A change to decoding, statistics, or the stream format shows up as a golden diff. Review the diff, then regenerate with `go test ./harness -update`.
<!-- /memoria:section -->

## Where each layer is tested

- `core` has plain unit tests for statistics, outcome classification, and finding triage.
- `harness` runs the fake runner through the real process code: golden streams, run records, pinned environment, failures, timeouts, interrupts, orphaned tools, preflight, and history. It also decodes the simple, parallel, and timeout captures directly.
- `cmd/uagent` builds the CLI and checks stdout, stderr, and exit codes for each output mode.
