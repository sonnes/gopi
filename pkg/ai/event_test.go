package ai_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
)

func TestEvent_JSONOmitsZeroFields(t *testing.T) {
	data, err := json.Marshal(ai.Event{Type: ai.EventStart})
	require.NoError(t, err)
	assert.JSONEq(t, `{"type":"start"}`, string(data))

	ev := ai.Event{
		Type:         ai.EventToolEnd,
		ContentIndex: 2,
		ToolCall:     &ai.ToolCall{ID: "c", Name: "n", Input: json.RawMessage(`{}`)},
	}
	data, err = json.Marshal(ev)
	require.NoError(t, err)

	var got ai.Event
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, ev, got)
}
