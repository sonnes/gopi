package session_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
)

func entry(id string, m ai.Message) session.Entry {
	return session.Entry{ID: id, Message: m}
}

func call(id string) ai.Message {
	return ai.AssistantMessage(ai.ToolCall{ID: id, Name: "t", Input: json.RawMessage(`{}`)})
}

func result(id string) ai.Message {
	return ai.ToolResultMessage(id, ai.Text{Text: "out " + id})
}

// sample is a base, a user message, a tool call and its result, a
// system change, and an answer.
func sample() []session.Entry {
	return []session.Entry{
		entry("base", ai.SystemMessage(ai.Text{Text: "prompt"})),
		entry("e1", ai.UserMessage("first")),
		entry("e2", call("c1")),
		entry("e3", result("c1")),
		entry("e4", ai.SystemMessage(ai.ToolRemoved{Name: "t"})),
		entry("e5", ai.AssistantMessage(ai.Text{Text: "done"})),
	}
}

func ids(view []session.Entry) []string {
	out := make([]string, 0, len(view))
	for _, e := range view {
		out = append(out, e.ID)
	}
	return out
}

func TestFold(t *testing.T) {
	summary := []ai.Message{ai.UserMessage("summary")}
	tests := []struct {
		name  string
		edits []session.Edit
		want  []string
	}{
		{
			name: "no edits",
			want: []string{"base", "e1", "e2", "e3", "e4", "e5"},
		},
		{
			name:  "a range becomes its replacement, with the ID of First",
			edits: []session.Edit{{First: "e2", Last: "e3", Messages: summary}},
			want:  []string{"base", "e1", "e2.0", "e4", "e5"},
		},
		{
			name:  "a system message in a range stays after the replacement",
			edits: []session.Edit{{First: "e2", Last: "e5", Messages: summary}},
			want:  []string{"base", "e1", "e2.0", "e4"},
		},
		{
			name: "a later edit names a replacement",
			edits: []session.Edit{
				{First: "e2", Last: "e3", Messages: summary},
				{First: "e2.0", Last: "e5", Messages: summary},
			},
			want: []string{"base", "e1", "e2.0.0", "e4"},
		},
		{
			name: "an edit with an unknown entry does not apply",
			edits: []session.Edit{
				{First: "gone", Last: "e3", Messages: summary},
			},
			want: []string{"base", "e1", "e2", "e3", "e4", "e5"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ids(session.Fold(sample(), tt.edits)))
		})
	}
}

func TestFoldKeepsEphemeral(t *testing.T) {
	view := sample()
	view = append(view[:3], append([]session.Entry{{
		ID:        "note",
		Ephemeral: true,
		Message:   ai.UserMessage("now"),
	}}, view[3:]...)...)

	got := session.Fold(view, []session.Edit{{
		First:    "e2",
		Last:     "e3",
		Messages: []ai.Message{ai.UserMessage("summary")},
	}})
	assert.Equal(t, []string{"base", "e1", "e2.0", "note", "e4", "e5"}, ids(got))
}

func TestCheckEdits(t *testing.T) {
	summary := []ai.Message{ai.UserMessage("summary")}
	tests := []struct {
		name  string
		edits []session.Edit
		want  string
	}{
		{
			name:  "a whole tool call and its result",
			edits: []session.Edit{{First: "e2", Last: "e3", Messages: summary}},
		},
		{
			name:  "a smaller result with the same call",
			edits: []session.Edit{{First: "e3", Last: "e3", Messages: []ai.Message{result("c1")}}},
		},
		{
			name:  "an unknown entry",
			edits: []session.Edit{{First: "nope", Last: "e3", Messages: summary}},
			want:  `session: edit 0: no entry "nope"`,
		},
		{
			name:  "First after Last",
			edits: []session.Edit{{First: "e3", Last: "e2", Messages: summary}},
			want:  `session: edit 0: "e3" comes after "e2"`,
		},
		{
			name: "an edit starts inside the range of an earlier edit",
			edits: []session.Edit{
				{First: "e2", Last: "e3", Messages: summary},
				{First: "e3", Last: "e5", Messages: summary},
			},
			want: `session: edit 1: no entry "e3"`,
		},
		{
			name:  "the base",
			edits: []session.Edit{{First: "base", Last: "e1", Messages: summary}},
			want:  "session: edit 0: the range has the base",
		},
		{
			name:  "a call with no result",
			edits: []session.Edit{{First: "e3", Last: "e3"}},
			want:  `session: tool call "c1" has no result`,
		},
		{
			name:  "a result with no call",
			edits: []session.Edit{{First: "e2", Last: "e2"}},
			want:  `session: tool result "c1" has no call`,
		},
		{
			name: "a replacement with thinking",
			edits: []session.Edit{{
				First: "e2",
				Last:  "e3",
				Messages: []ai.Message{
					ai.AssistantMessage(ai.ThinkingBlock{Thinking: "x", Signature: "s"}),
				},
			}},
			want: "session: edit 0: a replacement has a thinking block",
		},
		{
			name:  "a system replacement",
			edits: []session.Edit{{First: "e2", Last: "e3", Messages: []ai.Message{ai.SystemMessage(ai.Text{Text: "x"})}}},
			want:  "session: edit 0: a replacement is a system message",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := session.CheckEdits(sample(), tt.edits)
			if tt.want == "" {
				require.NoError(t, err)
				return
			}
			assert.EqualError(t, err, tt.want)
		})
	}
}

func TestCheckEditsRefusesAnEphemeralEntry(t *testing.T) {
	view := sample()
	view[1].Ephemeral = true
	err := session.CheckEdits(view, []session.Edit{{
		First:    "e1",
		Last:     "e1",
		Messages: []ai.Message{ai.UserMessage("x")},
	}})
	assert.EqualError(t, err, `session: edit 0: entry "e1" is ephemeral`)
}

func TestHistory(t *testing.T) {
	ctx := context.Background()
	l := session.New()

	_, err := l.Append(ctx, session.Text("first"))
	require.NoError(t, err)
	stored, err := l.Append(ctx, session.Text("second"))
	require.NoError(t, err)

	c := session.Compaction{
		Strategy: "summarize",
		Edits: []session.Edit{{
			First:    stored[0].ID,
			Last:     stored[0].ID,
			Messages: []ai.Message{ai.UserMessage("short")},
		}},
	}
	e, err := session.Custom(session.KindCompaction, c)
	require.NoError(t, err)
	_, err = l.Append(ctx, e)
	require.NoError(t, err)

	_, err = l.Append(ctx, session.NewEntry(call("c1")))
	require.NoError(t, err)

	h := l.History()
	require.Len(t, h.Entries, 4, "the log repairs the call with no result")
	assert.Equal(t, 2, h.CompactedAt, "the messages before the compaction")
	assert.Equal(t, c.Edits, h.Edits)

	last := h.Entries[3]
	assert.Equal(t, ai.RoleToolResult, last.Message.Role)
	assert.Equal(t, h.Entries[2].ID+"/c1", last.ID, "a repaired result has a fixed ID")
	assert.Equal(t, last.ID, l.History().Entries[3].ID)
	assert.Len(t, l.Messages(), 4)
}
