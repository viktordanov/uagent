package harness

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/viktordanov/uagent/core"
)

func TestRequestDTO_RoundTripsRoles(t *testing.T) {
	req := core.Request{Messages: []core.UserInput{
		{ID: "a", Text: "context", Role: core.RoleDeveloper},
		{ID: "b", Text: "hello"},
	}}

	dto := requestToDTO(req)

	assert.Equal(t, []requestMessageDTO{
		{Role: "developer", Content: "context", MessageID: "a"},
		{Role: "user", Content: "hello", MessageID: "b"},
	}, dto.Messages)
	assert.Equal(t, req.Messages, requestFromDTO(dto).Messages, "a user message reads back with an empty role")
}

func TestInputEvents_Developer(t *testing.T) {
	at := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	events := inputEvents(at, inputDTO{ID: "a", Kind: "developer", Payload: []byte(`"context"`)})

	assert.Equal(t, []core.Event{core.DeveloperMessage{At: at, ID: "a", Text: "context"}}, events)
}
