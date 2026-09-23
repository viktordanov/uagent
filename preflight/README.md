<!-- memoria:section id="overview" files="preflight.go" -->
# Preflight

<!-- memoria:export id="summary" -->
The preflight package checks the workspace, the state directory, the workspace .env file, and provider credentials before a run, and reports each problem as a domain finding.
<!-- /memoria:export -->

`New(Config{StateDir, Getenv})` returns a `domain.Preflight`. `Getenv` defaults to `os.Getenv`; tests replace it.
`Check` returns findings, not errors. It returns an error only when a file it must read exists but cannot be read, or when the home directory cannot be found.
<!-- /memoria:section -->

<!-- memoria:section id="contract" files="preflight.go" -->
## Findings

| Code | Severity | Cause |
| --- | --- | --- |
| `workspace_missing` | blocking | The workspace is not a directory. No other check runs. |
| `state_in_workspace` | blocking | The state directory is the workspace or inside it. The agent's own background searches can read the session output back and grow it without limit (unreal-agent issue #3). |
| `dotenv_risky` | blocking, or warning with `--allow-dotenv` | The workspace `.env` sets an `UNREAL_HARNESS_*`, `OPENAI_CODEX_*`, `CODEX_HOME`, or `*_PROXY` variable, which can redirect the model endpoint or credentials (unreal-agent issue #5). |
| `auth_missing` | blocking | No credentials for the provider: no Codex `auth.json` access token, or no API key variable. |
| `auth_expired` | blocking | The Codex access token has expired. The runner does not refresh it; run `codex login`. |
| `auth_expiring` | warning | The Codex access token expires within an hour. |

Paths are compared after resolving symlinks. For a state directory that does not exist yet, the nearest existing parent is resolved, so `/tmp` and `/private/tmp` compare equal on macOS.

Codex credentials come from `OPENAI_CODEX_ACCESS_TOKEN`, then `OPENAI_CODEX_AUTH_FILE`, then `$CODEX_HOME/auth.json`, then `~/.codex/auth.json`, the same order the runner uses.
Keyed providers need `UNREAL_HARNESS_LLM_API_KEY` or their own variable: `OPENAI_API_KEY`, `OPENROUTER_API_KEY`, or `FIREWORKS_API_KEY`. `ollama` needs none.
<!-- /memoria:section -->

<!-- memoria:section id="testing" files="preflight_test.go" -->
## Tests

`preflight_test.go` builds a temporary workspace and Codex home per case and covers each finding, including tokens that are expired or about to expire.
<!-- /memoria:section -->
