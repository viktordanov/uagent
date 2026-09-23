<!-- memoria:section id="overview" files="statedir.go" -->
# State directory

<!-- memoria:export id="summary" -->
The statedir package defines where uagent keeps runner sessions, logs, and per-run records, always outside the agent workspace.
<!-- /memoria:export -->

`Default()` is `$XDG_STATE_HOME/unreal-agent`, or `~/.local/state/unreal-agent`. `Layout{Root}` resolves every path under it:

```text
<root>/
├── sessions/                     runner session files (-session-directory)
│   ├── <session-id>.session.jsonl
│   └── operations/<session-id>/  background tool output, watched by --max-disk
├── logs/                         runner logs (-log-directory)
└── runs/<run-id>/
    ├── request.json              the request sent to the runner
    ├── events.jsonl              raw runner stdout
    ├── stderr.log                runner stderr
    └── summary.json              the finished run, in the stream summary schema
```
<!-- /memoria:section -->
