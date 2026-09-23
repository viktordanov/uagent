// Package runstore saves and loads run summaries under the state directory.
package runstore

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/viktordanov/uagent/domain"
	"github.com/viktordanov/uagent/statedir"
	"github.com/viktordanov/uagent/stream"
)

type store struct {
	layout statedir.Layout
}

func New(layout statedir.Layout) domain.RunStore {
	return &store{layout: layout}
}

// Save writes summary.json in the run directory, using the stream summary schema.
func (s *store) Save(_ context.Context, result domain.RunResult) error {
	encoded, err := json.MarshalIndent(stream.SummaryToDTO(result), "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode summary: %w", err)
	}
	path := filepath.Join(s.layout.RunDir(result.Request.RunID), statedir.SummaryFile)
	if err := os.WriteFile(path, append(encoded, '\n'), 0o600); err != nil {
		return fmt.Errorf("failed to write summary: %w", err)
	}

	return nil
}

// LoadSummary reads summary.json from a run directory. found is false when it does not exist.
func LoadSummary(runDir string) (result domain.RunResult, found bool, err error) {
	data, err := os.ReadFile(filepath.Join(runDir, statedir.SummaryFile))
	if os.IsNotExist(err) {
		return domain.RunResult{}, false, nil
	}
	if err != nil {
		return domain.RunResult{}, false, fmt.Errorf("failed to read summary: %w", err)
	}
	var dto stream.SummaryDTO
	if err := json.Unmarshal(data, &dto); err != nil {
		return domain.RunResult{}, false, fmt.Errorf("failed to decode summary: %w", err)
	}

	return stream.SummaryFromDTO(dto), true, nil
}
