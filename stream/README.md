<!-- memoria:section id="overview" files="stream.go" -->
# Stream

<!-- memoria:export id="summary" -->
The stream package writes run events as versioned JSONL for `uagent --stream`. It is the contract for programs that drive uagent from outside Go.
<!-- /memoria:export -->

1. [Read the stream](#read-the-stream)
2. [Events](#events)
3. [Summary](#summary)
4. [Compatibility](#compatibility)

`uagent --stream` writes one JSON object per line to stdout and nothing else to stdout.
Diagnostics and preflight errors still go to stderr. The process exit code follows the same table as a normal run.
<!-- /memoria:section -->

<!-- memoria:section id="usage" files="stream.go serialization.go" -->
## Read the stream

Every line has three common fields:

| Field | Meaning |
| --- | --- |
| `v` | Schema version. This document describes version 1. |
| `type` | Event type from the table below. |
| `at` | RFC 3339 timestamp in UTC. Runner events carry the runner's own recording time. |

A consumer reads lines until EOF. Once the runner has started, the stream ends with exactly one `run_finished` line, even when saving `summary.json` fails.
When preflight blocks the run, no line is written, stderr carries the reason, and the exit code is 2.
When the runner cannot start, the stream stops after `run_started`, stderr carries the reason, and the exit code is 1.
Ignore unknown `type` values and unknown fields so that additive changes do not break the reader.
<!-- /memoria:section -->

<!-- memoria:section id="events" files="serialization.go" -->
## Events

| Type | Fields | Meaning |
| --- | --- | --- |
| `run_started` | `run_id`, `session_id`, `provider`, `model`, `effort`, `workspace` | Always first. `run_id` names the run directory. |
| `preflight_warning` | `code`, `message` | A non-blocking preflight finding, such as `auth_expiring`. |
| `user_message` | `id`, `text` | The runner accepted a user message. `id` is the message ID it deduplicates on, so it acknowledges delivery of a message sent with that ID. |
| `control_input` | `id`, `mode`, `effort`, `reason` | The runner accepted a control message: `settings` (with `effort`), `when_idle`, `hard`, or `heartbeat` (with `reason`). |
| `turn_started` | `turn`, `turn_id` | The runner sent a request to the model. Turns count from 1; `turn_id` is the runner's ID for the turn. |
| `model_responded` | `turn`, `turn_id`, `duration_ms`, `usage`, `stop`, `failure` | The model answered. `usage` has `input`, `cached_input`, `cache_write_input`, `output`, and `reasoning` token counts. `stop` and `failure` are omitted when normal. |
| `tool_called` | `call_id`, `name`, `label`, `arguments` | The model asked for a tool. `label` is the Bash command or the raw arguments, on one line; `arguments` is the raw JSON. |
| `tool_started` | `call_id`, `op_id`, `name`, `label`, `op_type` | The runner started the operation for a call. `op_type` is the runner's operation type, such as `shell`. |
| `tool_finished` | `call_id`, `op_id`, `name`, `label`, `ok`, `detail`, `duration_ms`, `op_type`, `out_path`, `err_path` | The operation ended. `detail` is `exit N`, a status, or an error. `op_id` is omitted when the call failed before an operation started. `out_path` and `err_path` name the files holding a shell command's full output. |
| `assistant_message` | `turn`, `text`, `final` | Assistant text from a turn. `final: true` marks the final answer. |
| `reasoning` | `turn`, `text` | One reasoning summary line. |
| `runner_error` | `message` | The runner reported an error or printed a line that is not JSON. |
| `run_finished` | `summary` | Always last. See the summary below. |

A tool that is still running when the run is killed has a `tool_started` line and no `tool_finished` line.
The runner reports output files once an operation has finished, so `out_path` and `err_path` appear on `tool_finished` and are usually absent from `tool_started`.
<!-- /memoria:section -->

<!-- memoria:section id="contract" files="serialization.go" -->
## Summary

`summary` in `run_finished` has the same shape as `summary.json` in the run directory:

| Field | Meaning |
| --- | --- |
| `status` | `ok`, `error`, `timeout`, `interrupted`, or `disk_limit`; `running` in `summary.json` while the run is in progress |
| `runner_exit_code` | Exit code of unreal-agent-runner, or -1 when it was killed |
| `run_id`, `session_id`, `provider`, `model`, `effort`, `workspace` | The run's identity and settings |
| `started_at`, `wall_ms` | Start time (UTC) and wall-clock duration |
| `stats` | Aggregated statistics, described below |
| `answer` | The final answer, or the last assistant text when there was none |

`stats` has `event_span_ms`, `model_ms`, `tool_busy_ms`, `tool_model_overlap_ms`, `turns`, `user_messages`, `model_responses`, `tool_calls`, `failed_tool_calls`, `max_parallel_tools`, `tools_by_name`, `tokens`, and `final_answer`.
It also has `stop_reasons`, `failures`, `errors`, and `warnings`, which are omitted when empty.
`tool_model_overlap_ms` is the time tools ran while the model was generating, which is where the runner's asynchronous tools save time.

## Compatibility

The schema version changes only for a breaking change: a removed or renamed field, a changed type, or a changed meaning.
New event types and new fields are additive and keep version 1. `uagent --version` reports the build that produced a stream.
<!-- /memoria:section -->
