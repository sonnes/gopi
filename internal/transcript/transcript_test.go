package transcript

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sonnes/gopi/pkg/ai"
)

func TestSystemText(t *testing.T) {
	got := SystemText([]ai.Content{
		ai.Text{Text: "Be careful."},
		ai.Text{Text: ""},
		ai.Section{Name: "rules", Text: "<rules>Cite.</rules>"},
		ai.SectionRemoved{Name: "todos"},
		ai.ToolRemoved{Name: "bash"},
	})
	assert.Equal(
		t,
		"Be careful.\n\n"+
			"Updated system prompt section \"rules\":\n\n<rules>Cite.</rules>\n\n"+
			"Removed system prompt section \"todos\".",
		got,
	)
}

func TestReminder(t *testing.T) {
	assert.Equal(t, "<system-reminder>\nCite.\n</system-reminder>", Reminder("Cite."))
}

func TestCloseToolCalls(t *testing.T) {
	c1 := ai.ToolCall{ID: "c1", Name: "calc", Input: json.RawMessage(`{}`)}
	c2 := ai.ToolCall{ID: "c2", Name: "calc", Input: json.RawMessage(`{}`)}
	noResult := func(id string) ai.Message {
		m := ai.ToolResultMessage(id, ai.Text{Text: NoResultText})
		m.IsError = true
		return m
	}
	system := ai.SystemMessage(ai.Text{Text: "Be brief."})

	tests := []struct {
		name     string
		messages []ai.Message
		want     []ai.Message
	}{
		{
			name: "a user message interrupts the calls",
			messages: []ai.Message{
				ai.AssistantMessage(c1, c2),
				ai.ToolResultMessage("c1", ai.Text{Text: "4"}),
				system,
				ai.UserMessage("Never mind"),
			},
			want: []ai.Message{
				ai.AssistantMessage(c1, c2),
				ai.ToolResultMessage("c1", ai.Text{Text: "4"}),
				noResult("c2"),
				system,
				ai.UserMessage("Never mind"),
			},
		},
		{
			name:     "the messages end with the calls",
			messages: []ai.Message{ai.AssistantMessage(c1)},
			want:     []ai.Message{ai.AssistantMessage(c1), noResult("c1")},
		},
		{
			name: "every call has a result",
			messages: []ai.Message{
				ai.AssistantMessage(c1),
				ai.ToolResultMessage("c1", ai.Text{Text: "4"}),
			},
			want: []ai.Message{
				ai.AssistantMessage(c1),
				ai.ToolResultMessage("c1", ai.Text{Text: "4"}),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, CloseToolCalls(tt.messages))
		})
	}
}

func TestCurrentTools(t *testing.T) {
	read := ai.ToolInfo{Name: "read"}
	bash := ai.ToolInfo{Name: "bash"}
	weather := ai.ToolInfo{Name: "weather"}
	newRead := ai.ToolInfo{Name: "read", Description: "Read a file"}

	tests := []struct {
		name     string
		messages []ai.Message
		want     []ai.ToolInfo
	}{
		{
			name: "no changes",
			messages: []ai.Message{
				ai.UserMessage("hi"),
			},
			want: []ai.ToolInfo{read, bash},
		},
		{
			name: "add and remove",
			messages: []ai.Message{
				ai.SystemMessage(ai.ToolRemoved{Name: "bash"}, ai.ToolAdded{Tool: weather}),
			},
			want: []ai.ToolInfo{read, weather},
		},
		{
			name: "replace in place",
			messages: []ai.Message{
				ai.SystemMessage(ai.ToolAdded{Tool: newRead}),
			},
			want: []ai.ToolInfo{newRead, bash},
		},
		{
			name: "remove then add again",
			messages: []ai.Message{
				ai.SystemMessage(ai.ToolRemoved{Name: "read"}),
				ai.UserMessage("hi"),
				ai.SystemMessage(ai.ToolAdded{Tool: read}),
			},
			want: []ai.ToolInfo{bash, read},
		},
		{
			name: "blocks outside a system message",
			messages: []ai.Message{
				{
					Role:    ai.RoleUser,
					Content: []ai.Content{ai.ToolRemoved{Name: "bash"}},
				},
			},
			want: []ai.ToolInfo{read, bash},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CurrentTools([]ai.ToolInfo{read, bash}, tt.messages)
			assert.Equal(t, tt.want, got)
		})
	}
}
