package harness

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/viktordanov/uagent/core"
	"github.com/viktordanov/uagent/stream"
)

// Files in a run directory.
const (
	RequestFile = "request.json"
	EventsFile  = "events.jsonl"
	StderrFile  = "stderr.log"
	SummaryFile = "summary.json"
)

// DefaultStateDir is $XDG_STATE_HOME/unreal-agent or ~/.local/state/unreal-agent.
// It holds runner sessions, logs, and run records, always outside the workspace.
func DefaultStateDir() string {
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "unreal-agent")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".uagent-state")
	}

	return filepath.Join(home, ".local", "state", "unreal-agent")
}

// layout resolves paths under one state root.
type layout struct{ root string }

func (l layout) sessionsDir() string        { return filepath.Join(l.root, "sessions") }
func (l layout) logsDir() string            { return filepath.Join(l.root, "logs") }
func (l layout) runsDir() string            { return filepath.Join(l.root, "runs") }
func (l layout) runDir(runID string) string { return filepath.Join(l.runsDir(), runID) }
func (l layout) sessionFile(id string) string {
	return filepath.Join(l.sessionsDir(), id+".session.jsonl")
}

func (l layout) operationsDir(id string) string {
	return filepath.Join(l.sessionsDir(), "operations", id)
}

// saveSummary writes summary.json in the stream summary schema.
func (l layout) saveSummary(result core.Result) error {
	encoded, err := json.MarshalIndent(stream.SummaryToDTO(result), "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode summary: %w", err)
	}
	path := filepath.Join(l.runDir(result.Request.RunID), SummaryFile)
	if err := os.WriteFile(path, append(encoded, '\n'), 0o600); err != nil {
		return fmt.Errorf("failed to write summary: %w", err)
	}

	return nil
}

// History lists saved runs, newest first. Runs without a readable summary are skipped.
func (h *Harness) History() ([]core.Result, error) {
	entries, err := os.ReadDir(h.layout.runsDir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to list runs: %w", err)
	}
	var results []core.Result
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		result, found, err := loadSummary(h.layout.runDir(e.Name()))
		if err != nil || !found {
			continue
		}
		results = append(results, result)
	}
	slices.SortFunc(results, func(a, b core.Result) int { return b.StartedAt.Compare(a.StartedAt) })

	return results, nil
}

// RunDir returns the directory of a run.
func (h *Harness) RunDir(runID string) string { return h.layout.runDir(runID) }

// Load reads a saved run from a run directory or an events.jsonl path. Stats and
// the answer are recomputed from the events; metadata comes from summary.json
// when it exists.
func Load(path string) (core.Result, error) {
	var result core.Result
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		saved, found, err := loadSummary(path)
		if err != nil {
			return core.Result{}, err
		}
		if found {
			result = saved
		}
		path = filepath.Join(path, EventsFile)
	}
	f, err := os.Open(path)
	if err != nil {
		return core.Result{}, fmt.Errorf("failed to open events: %w", err)
	}
	defer f.Close()
	collector := core.NewStatsCollector()
	if err := ReadEvents(f, collector.Add); err != nil {
		return core.Result{}, err
	}
	result.Stats, result.Answer = collector.Stats(), collector.Answer()
	if result.Status == "" {
		result.Status = core.Classify(core.TerminationExited, 0, collector.HasError())
		result.Wall = result.Stats.EventSpan
	}

	return result, nil
}

func loadSummary(runDir string) (result core.Result, found bool, err error) {
	data, err := os.ReadFile(filepath.Join(runDir, SummaryFile))
	if errors.Is(err, fs.ErrNotExist) {
		return core.Result{}, false, nil
	}
	if err != nil {
		return core.Result{}, false, fmt.Errorf("failed to read summary: %w", err)
	}
	var dto stream.SummaryDTO
	if err := json.Unmarshal(data, &dto); err != nil {
		return core.Result{}, false, fmt.Errorf("failed to decode summary: %w", err)
	}

	return stream.SummaryFromDTO(dto), true, nil
}
