package session_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
)

func TestText(t *testing.T) {
	e := session.Text("hi")

	assert.Equal(t, ai.RoleUser, e.Message.Role)
	assert.Equal(t, "hi", e.Message.Text())
	assert.Empty(t, e.Author)
	assert.False(t, e.Ephemeral)
	assert.Empty(t, e.ID, "the log assigns the ID")
}

func TestEntryOptions(t *testing.T) {
	tests := []struct {
		name      string
		opts      []session.Option
		author    string
		ephemeral bool
	}{
		{
			name:   "author",
			opts:   []session.Option{session.Author("user-42")},
			author: "user-42",
		},
		{
			name:      "ephemeral",
			opts:      []session.Option{session.Ephemeral()},
			ephemeral: true,
		},
		{
			name:      "combined",
			opts:      []session.Option{session.Author("harness"), session.Ephemeral()},
			author:    "harness",
			ephemeral: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := session.Text("x", tt.opts...)
			assert.Equal(t, tt.author, e.Author)
			assert.Equal(t, tt.ephemeral, e.Ephemeral)
		})
	}
}

func TestNewEntry(t *testing.T) {
	m := ai.AssistantMessage(ai.Text{Text: "ok"})
	e := session.NewEntry(m, session.Author("bot"))

	assert.Equal(t, m, e.Message)
	assert.Equal(t, "bot", e.Author)
}

func TestEntryJSON(t *testing.T) {
	e := session.Text("hi", session.Author("user-42"), session.Ephemeral())
	e.ID = "e1"

	data, err := json.Marshal(e)
	require.NoError(t, err)

	var got session.Entry
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, "e1", got.ID)
	assert.Equal(t, "user-42", got.Author)
	assert.True(t, got.Ephemeral)
	assert.Equal(t, "hi", got.Message.Text())
}

func TestCustom(t *testing.T) {
	type summary struct {
		Text string `json:"text"`
	}

	e, err := session.Custom("compaction", summary{Text: "short"}, session.Author("compactor"))
	require.NoError(t, err)

	assert.Equal(t, "compaction", e.Kind)
	assert.JSONEq(t, `{"text":"short"}`, string(e.Data))
	assert.Equal(t, "compactor", e.Author)
	assert.Zero(t, e.Message, "the model never reads a custom entry")

	data, err := json.Marshal(e)
	require.NoError(t, err)
	assert.NotContains(t, string(data), `"message"`)

	var got session.Entry
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, e.Kind, got.Kind)
	assert.JSONEq(t, string(e.Data), string(got.Data))
}

func TestCustomErrors(t *testing.T) {
	tests := []struct {
		name string
		kind string
		data any
	}{
		{
			name: "empty kind",
			kind: "",
			data: 1,
		},
		{
			name: "data that does not encode",
			kind: "x",
			data: make(chan int),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := session.Custom(tt.kind, tt.data)
			assert.Error(t, err)
		})
	}
}
