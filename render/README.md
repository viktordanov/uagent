<!-- memoria:section id="overview" files="palette.go progress.go summary.go" -->
# Render

<!-- memoria:export id="summary" -->
The render package formats run events and summaries for people reading a terminal: live progress lines and the end-of-run summary block.
<!-- /memoria:export -->

`NewProgress(w, palette, verbose)` returns a `domain.EventSink` that prints one line per turn, tool call, tool result, and message, prefixed with the time since the run started.
Reasoning summaries appear only when `verbose` is true.
`Summary(w, result, runDir, palette)` prints status, model, time split, turns and tools, tokens, and any stop reasons, failures, or errors.

`PaletteFor(file)` enables ANSI color only when the file is a terminal and `NO_COLOR` is unset.
Output goes to the writer it is given; `cmd/uagent` passes stderr so stdout stays free for the answer.
<!-- /memoria:section -->
