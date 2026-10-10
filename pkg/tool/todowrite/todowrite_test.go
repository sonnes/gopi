package todowrite

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
)

// call runs the tool with in and returns the result and its text.
func call(t *testing.T, in Input) (ai.ToolResult, string) {
	t.Helper()

	raw, err := json.Marshal(in)
	require.NoError(t, err)

	res, err := New().Run(
		t.Context(),
		ai.ToolCall{
			ID:    "call-1",
			Name:  Name,
			Input: raw,
		},
	)
	require.NoError(t, err)
	require.Len(t, res.Content, 1)

	text, ok := res.Content[0].(ai.Text)
	require.True(t, ok)
	return res, text.Text
}

func item(content, active string, status Status) Item {
	return Item{
		Content:    content,
		ActiveForm: active,
		Status:     status,
	}
}

func TestInfo(t *testing.T) {
	info := New().Info()

	assert.Equal(t, "todo_write", info.Name)
	assert.Equal(t, Description, info.Description)
	assert.NotNil(t, info.InputSchema)
}

func TestRun_AcceptsValidList(t *testing.T) {
	tests := []struct {
		name  string
		todos []Item
	}{
		{
			name: "pending and in progress",
			todos: []Item{
				item("Write tests", "Writing tests", StatusInProgress),
				item("Ship it", "Shipping it", StatusPending),
			},
		},
		{
			name: "single item",
			todos: []Item{
				item("Do the thing", "Doing the thing", StatusInProgress),
			},
		},
		{
			name:  "empty list clears the checklist",
			todos: []Item{},
		},
		{
			name: "all completed",
			todos: []Item{
				item("First", "Doing first", StatusCompleted),
				item("Second", "Doing second", StatusCompleted),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, text := call(t, Input{Todos: tc.todos})

			assert.False(t, res.IsError, text)
			assert.Contains(t, strings.ToLower(text), "successfully")
		})
	}
}

func TestRun_RejectsInvalidList(t *testing.T) {
	tests := []struct {
		name  string
		todos []Item
		want  string
	}{
		{
			name: "missing content",
			todos: []Item{
				item("", "Doing it", StatusPending),
			},
			want: "content",
		},
		{
			name: "missing active form",
			todos: []Item{
				item("Do it", "", StatusPending),
			},
			want: "active_form",
		},
		{
			name: "invalid status",
			todos: []Item{
				item("Do it", "Doing it", Status("blocked")),
			},
			want: "status",
		},
		{
			name: "more than one in progress",
			todos: []Item{
				item("First", "Doing first", StatusInProgress),
				item("Second", "Doing second", StatusInProgress),
			},
			want: "in_progress",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, text := call(t, Input{Todos: tc.todos})

			assert.True(t, res.IsError)
			assert.Contains(t, strings.ToLower(text), tc.want)
			assert.Contains(t, text, "Expected shape:", "the error helps the model correct the call")
		})
	}
}

func TestRun_RejectsAMissingList(t *testing.T) {
	res, text := call(t, Input{})

	assert.True(t, res.IsError)
	assert.Contains(t, text, "todos is required")
}
