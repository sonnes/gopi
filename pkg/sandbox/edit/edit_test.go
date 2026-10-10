package edit

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/sandbox/internal/tooltest"
)

func TestEdit(t *testing.T) {
	tests := []struct {
		name    string
		content string
		input   map[string]any
		want    string
		wantErr string
	}{
		{
			name:    "replacement",
			content: "hello world",
			input:   map[string]any{"old_string": "world", "new_string": "universe"},
			want:    "hello universe",
		},
		{
			name:    "replace all",
			content: "a b a",
			input:   map[string]any{"old_string": "a", "new_string": "c", "replace_all": true},
			want:    "c b c",
		},
		{
			name:    "keeps CRLF line endings",
			content: "a\r\nb\r\nc\r\n",
			input:   map[string]any{"old_string": "a\nb", "new_string": "A\nB"},
			want:    "A\r\nB\r\nc\r\n",
		},
		{
			name:    "mixed line endings",
			content: "a\r\nb\nc\n",
			input:   map[string]any{"old_string": "b\nc", "new_string": "B\nC"},
			want:    "a\r\nB\nC\n",
		},
		{
			name:    "not found",
			content: "hello world",
			input:   map[string]any{"old_string": "nope", "new_string": "x"},
			wantErr: "old_string not found",
		},
		{
			name:    "more than one match",
			content: "a b a",
			input:   map[string]any{"old_string": "a", "new_string": "c"},
			wantErr: "old_string occurs 2 times",
		},
		{
			name:    "empty old string",
			content: "hello world",
			input:   map[string]any{"old_string": "", "new_string": "x"},
			wantErr: "old_string is required",
		},
		{
			name:    "no change",
			content: "hello world",
			input:   map[string]any{"old_string": "world", "new_string": "world"},
			wantErr: "old_string and new_string are the same",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tooltest.WriteFiles(t, dir, map[string]string{"f.txt": tt.content})
			tt.input["path"] = "f.txt"

			got, isErr := tooltest.Call(t, New(tooltest.Local(t, dir)), tt.input)

			if tt.wantErr != "" {
				require.True(t, isErr, got)
				assert.Contains(t, got, tt.wantErr)
				assert.Equal(t, tt.content, tooltest.ReadFile(t, dir, "f.txt"))
				return
			}
			require.False(t, isErr, got)
			assert.Equal(t, "Edited f.txt", got)
			assert.Equal(t, tt.want, tooltest.ReadFile(t, dir, "f.txt"))
		})
	}
}

func TestEditMissingFile(t *testing.T) {
	got, isErr := tooltest.Call(t, New(tooltest.Local(t, t.TempDir())), map[string]any{
		"path":       "missing.txt",
		"old_string": "a",
		"new_string": "b",
	})

	require.True(t, isErr)
	assert.Contains(t, got, "no such file")
}
