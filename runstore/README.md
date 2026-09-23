<!-- memoria:section id="overview" files="runstore.go" -->
# Run store

<!-- memoria:export id="summary" -->
The runstore package saves each finished run as summary.json in its run directory and loads saved summaries for uagent stats.
<!-- /memoria:export -->

`New(layout)` returns a `domain.RunStore`. `Save` writes the run summary in the `stream` summary schema, so `summary.json` matches the `run_finished` payload.
`LoadSummary(runDir)` returns the saved run metadata and `found=false` when no summary exists. It does not restore statistics; callers recompute them from `events.jsonl`.
<!-- /memoria:section -->
