// Package stream writes run events as versioned JSONL, one object per line.
// It is the contract for programs that drive uagent, such as rs-uagent-tui.
package stream

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/viktordanov/uagent/core"
)

// Sink writes each event as one JSON line.
type Sink struct {
	enc *json.Encoder
	err error
}

func NewSink(w io.Writer) *Sink {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)

	return &Sink{enc: enc}
}

func (s *Sink) Emit(event core.Event) {
	dto, ok := EventToDTO(event)
	if !ok || s.err != nil {
		return
	}
	if err := s.enc.Encode(dto); err != nil {
		s.err = fmt.Errorf("failed to write stream event: %w", err)
	}
}

// Err returns the first write error, if any.
func (s *Sink) Err() error { return s.err }
