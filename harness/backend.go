package harness

import (
	"context"
	"io"

	"github.com/viktordanov/uagent/core"
)

// A Backend runs the agent for one run. The default, RunnerBackend, spawns
// unreal-agent-runner; another backend can run the runner's packages in
// process. Either way the harness keeps the guards, the session lock, the run
// records, and the statistics.
type Backend interface {
	Start(ctx context.Context, launch Launch) (Process, error)
}

// Launch is what a backend gets for one run.
type Launch struct {
	Request core.Request
	// RunnerRequest is the runner's JSON request, as saved in request.json.
	RunnerRequest []byte
	// SessionsDir and LogsDir are the runner's -session-directory and -log-directory.
	SessionsDir string
	LogsDir     string
	// Stdout receives the runner's JSONL output. The backend closes it once it
	// will write no more, at the latest when the process is done.
	Stdout io.WriteCloser
	// Stderr receives diagnostics; it is saved as stderr.log.
	Stderr io.Writer
}

// Process is a started agent. Its methods are safe to call from any goroutine
// and more than once.
type Process interface {
	// Done is closed when the agent has stopped.
	Done() <-chan struct{}
	// ExitCode is the runner's exit code once Done is closed: 0 for success,
	// 130 after an interrupt, and 1 after an error it reported.
	ExitCode() int
	// Interrupt asks the agent to stop and record its state, as SIGINT does.
	Interrupt()
	// Terminate stops the agent and its tools, as SIGTERM does.
	Terminate()
	// Kill stops the agent at once, as SIGKILL does.
	Kill()
}
