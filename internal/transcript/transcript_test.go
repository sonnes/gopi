package transcript

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

func TestOrder(t *testing.T) {
	call := ai.ToolCall{ID: "c1", Name: "calc", Input: json.RawMessage(`{}`)}
	result := ai.ToolResultMessage("c1", ai.Text{Text: "4"})
	rules := ai.SystemMessage(ai.Section{Name: "rules", Text: "Cite."})
	reminder := ai.Text{Text: Reminder(SystemText(rules.Content))}
	user := func(content ...ai.Content) ai.Message {
		return ai.Message{Role: ai.RoleUser, Content: content}
	}
	hi := ai.Text{Text: "Hi"}
	answer := ai.AssistantMessage(ai.Text{Text: "Hello"})

	tests := []struct {
		name           string
		systemMessages bool
		messages       []ai.Message
		want           []ai.Message
	}{
		{
			name:           "a system message waits for the next assistant message",
			systemMessages: true,
			messages:       []ai.Message{ai.AssistantMessage(call), rules, result, user(hi), answer},
			want:           []ai.Message{ai.AssistantMessage(call), result, user(hi), rules, answer},
		},
		{
			name:           "a system message waits for the end",
			systemMessages: true,
			messages:       []ai.Message{rules, user(hi)},
			want:           []ai.Message{user(hi), rules},
		},
		{
			name:     "a reminder starts the next user message",
			messages: []ai.Message{user(hi), rules, user(ai.Text{Text: "Go"})},
			want:     []ai.Message{user(hi), user(reminder, ai.Text{Text: "Go"})},
		},
		{
			name:     "a reminder ends the last user message",
			messages: []ai.Message{user(hi), rules, answer},
			want:     []ai.Message{user(hi, reminder), answer},
		},
		{
			name:     "a reminder after the tool results is a new user message",
			messages: []ai.Message{ai.AssistantMessage(call), rules, result},
			want:     []ai.Message{ai.AssistantMessage(call), result, user(reminder)},
		},
		{
			name:     "a system message with no text sends no reminder",
			messages: []ai.Message{user(hi), ai.SystemMessage(ai.ToolRemoved{Name: "bash"})},
			want:     []ai.Message{user(hi)},
		},
		{
			name:           "a call with no result gets one",
			systemMessages: true,
			messages:       []ai.Message{ai.AssistantMessage(call)},
			want:           CloseToolCalls([]ai.Message{ai.AssistantMessage(call)}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Order(tt.messages, tt.systemMessages))
		})
	}
}

func TestOrder_KeepsTheInput(t *testing.T) {
	messages := []ai.Message{
		ai.UserMessage("Hi"),
		ai.SystemMessage(ai.Text{Text: "Cite."}),
	}
	before := slices.Clone(messages[0].Content)

	Order(messages, false)
	assert.Equal(t, before, messages[0].Content)
}

func TestChangesOf(t *testing.T) {
	read := ai.ToolInfo{Name: "read"}
	bash := ai.ToolInfo{Name: "bash"}
	weather := ai.ToolInfo{Name: "weather"}
	newRead := ai.ToolInfo{Name: "read", Description: "Read a file"}

	tests := []struct {
		name        string
		messages    []ai.Message
		want        ToolChanges
		wantAllowed []string
	}{
		{
			name: "no changes",
			want: ToolChanges{
				Current:  []ai.ToolInfo{read, bash},
				Declared: []ai.ToolInfo{read, bash},
			},
		},
		{
			name: "an addition",
			messages: []ai.Message{
				ai.SystemMessage(ai.ToolAdded{Tool: weather}),
			},
			want: ToolChanges{
				Current:  []ai.ToolInfo{read, bash, weather},
				Declared: []ai.ToolInfo{read, bash, weather},
			},
		},
		{
			name: "a removal",
			messages: []ai.Message{
				ai.SystemMessage(ai.ToolRemoved{Name: "bash"}),
			},
			want: ToolChanges{
				Current:  []ai.ToolInfo{read},
				Declared: []ai.ToolInfo{read, bash},
				Removed:  true,
			},
			wantAllowed: []string{"read"},
		},
		{
			name: "a replacement",
			messages: []ai.Message{
				ai.SystemMessage(ai.ToolAdded{Tool: newRead}),
			},
			want: ToolChanges{
				Current:  []ai.ToolInfo{newRead, bash},
				Declared: []ai.ToolInfo{read, bash},
				Replaced: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ChangesOf([]ai.ToolInfo{read, bash}, tt.messages)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantAllowed, got.Allowed())
		})
	}
}

func TestLimitChoice(t *testing.T) {
	read := ai.ToolChoiceTool("read")
	tests := []struct {
		name      string
		choice    ai.ToolChoiceMode
		set       bool
		allowed   []string
		want      ai.ToolChoiceMode
		wantSend  bool
		wantLimit bool
	}{
		{name: "no limit and no choice"},
		{name: "no limit", choice: ai.ToolChoiceAny, set: true, want: ai.ToolChoiceAny, wantSend: true},
		{
			name:      "a limit with no choice",
			allowed:   []string{"read"},
			want:      ai.ToolChoiceAuto,
			wantSend:  true,
			wantLimit: true,
		},
		{
			name:      "a limit that the model must use",
			choice:    ai.ToolChoiceAny,
			set:       true,
			allowed:   []string{"read"},
			want:      ai.ToolChoiceAny,
			wantSend:  true,
			wantLimit: true,
		},
		{
			name:     "a named tool is narrower than the limit",
			choice:   read,
			set:      true,
			allowed:  []string{"bash"},
			want:     read,
			wantSend: true,
		},
		{
			name:     "no allowed tool",
			allowed:  []string{},
			want:     ai.ToolChoiceNone,
			wantSend: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, send, limit := LimitChoice(tt.choice, tt.set, tt.allowed)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantSend, send)
			assert.Equal(t, tt.wantLimit, limit)
		})
	}
}

func TestSchema(t *testing.T) {
	got, err := Schema(&jsonschema.Schema{Type: "object"})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"type": "object"}, got)

	got, err = Schema(nil)
	require.NoError(t, err)
	assert.Nil(t, got)

	_, err = Schema(&jsonschema.Schema{Type: "object", Types: []string{"object"}})
	require.Error(t, err)
}

func TestParameters(t *testing.T) {
	tests := []struct {
		name   string
		schema *jsonschema.Schema
		want   map[string]any
	}{
		{
			name:   "nil schema gives an empty object",
			schema: nil,
			want: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			name:   "schema gives the schema",
			schema: &jsonschema.Schema{Type: "object"},
			want:   map[string]any{"type": "object"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parameters(tt.schema)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParameters_NewMapEachCall(t *testing.T) {
	first, err := Parameters(nil)
	require.NoError(t, err)
	first["type"] = "string"

	second, err := Parameters(nil)
	require.NoError(t, err)
	assert.Equal(t, "object", second["type"])
}
