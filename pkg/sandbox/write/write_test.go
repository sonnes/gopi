package write

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/sandbox/internal/tooltest"
)

func TestWrite(t *testing.T) {
	tests := []struct {
		name    string
		files   map[string]string
		input   map[string]any
		want    string
		wantErr string
		check   map[string]string
	}{
		{
			name:  "new file and its parents",
			input: map[string]any{"path": "a/b/new.txt", "content": "hello"},
			want:  "Wrote 5 bytes to a/b/new.txt",
			check: map[string]string{"a/b/new.txt": "hello"},
		},
		{
			name:  "overwrites a file",
			files: map[string]string{"f.txt": "old content"},
			input: map[string]any{"path": "f.txt", "content": "new"},
			want:  "Wrote 3 bytes to f.txt",
			check: map[string]string{"f.txt": "new"},
		},
		{
			name:  "empty content",
			input: map[string]any{"path": "empty.txt", "content": ""},
			want:  "Wrote 0 bytes to empty.txt",
			check: map[string]string{"empty.txt": ""},
		},
		{
			name:    "no path",
			input:   map[string]any{"content": "x"},
			wantErr: "path is required",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tooltest.WriteFiles(t, dir, tt.files)

			got, isErr := tooltest.Call(t, New(tooltest.Local(t, dir)), tt.input)

			if tt.wantErr != "" {
				require.True(t, isErr, got)
				assert.Contains(t, got, tt.wantErr)
				return
			}
			require.False(t, isErr, got)
			assert.Equal(t, tt.want, got)
			for name, content := range tt.check {
				assert.Equal(t, content, tooltest.ReadFile(t, dir, name))
			}
		})
	}
}

func TestWriteRejectsAPathThatThePolicyDoesNotAllow(t *testing.T) {
	dir := t.TempDir()

	got, isErr := tooltest.Call(t, New(tooltest.Hidden(t, dir)), map[string]any{"path": "secret/key", "content": "x"})

	require.True(t, isErr)
	assert.Contains(t, got, "hidden")
}
