// Package statedir defines where uagent keeps sessions, logs, and run records.
// Everything lives outside the agent workspace (unreal-agent issue #3).
package statedir

import (
	"os"
	"path/filepath"
)

// Layout resolves paths under one state root.
type Layout struct {
	Root string
}

// Default is $XDG_STATE_HOME/unreal-agent or ~/.local/state/unreal-agent.
func Default() string {
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "unreal-agent")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".uagent-state")
	}

	return filepath.Join(home, ".local", "state", "unreal-agent")
}

func (l Layout) SessionsDir() string { return filepath.Join(l.Root, "sessions") }
func (l Layout) LogsDir() string     { return filepath.Join(l.Root, "logs") }
func (l Layout) RunsDir() string     { return filepath.Join(l.Root, "runs") }

// RunDir holds request.json, events.jsonl, stderr.log, and summary.json for one run.
func (l Layout) RunDir(runID string) string { return filepath.Join(l.RunsDir(), runID) }

// SessionFile is the runner's own session record, including background tool process groups.
func (l Layout) SessionFile(sessionID string) string {
	return filepath.Join(l.SessionsDir(), sessionID+".session.jsonl")
}

// OperationsDir holds the output files of one session's background tools.
func (l Layout) OperationsDir(sessionID string) string {
	return filepath.Join(l.SessionsDir(), "operations", sessionID)
}

const (
	RequestFile = "request.json"
	EventsFile  = "events.jsonl"
	StderrFile  = "stderr.log"
	SummaryFile = "summary.json"
)
