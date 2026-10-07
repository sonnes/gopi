package ai_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
)

func TestNewEventStream(t *testing.T) {
	s := ai.NewEventStream(func(push func(ai.Event)) (*ai.Message, error) {
		push(ai.Event{Type: ai.EventStart})
		push(ai.Event{Type: ai.EventTextDelta, Delta: "hi"})
		msg := ai.AssistantMessage(ai.Text{Text: "hi"})
		return &msg, nil
	})

	var types []ai.EventType
	for ev, err := range s.Events() {
		require.NoError(t, err)
		types = append(types, ev.Type)
	}
	assert.Equal(t, []ai.EventType{ai.EventStart, ai.EventTextDelta}, types)

	msg, err := s.Wait()
	require.NoError(t, err)
	assert.Equal(t, "hi", msg.Text())
}

func TestErrStream(t *testing.T) {
	want := errors.New("no key")
	msg, err := ai.ErrStream(want).Wait()
	assert.ErrorIs(t, err, want)
	assert.Nil(t, msg)
}
