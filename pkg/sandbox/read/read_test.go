package read

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/sandbox/internal/tooltest"
)

// numbered returns the lines "line from" to "line to", one on each line.
func numbered(from, to int) string {
	var lines []string
	for i := from; i <= to; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	return strings.Join(lines, "\n")
}

func TestReadText(t *testing.T) {
	wideLine := strings.Repeat("x", 1000)
	wide := strings.Repeat(wideLine+"\n", 60)

	dir := t.TempDir()
	tooltest.WriteFiles(t, dir, map[string]string{
		"three.txt": "one\ntwo\nthree\n",
		"empty.txt": "",
		"long.txt":  numbered(1, maxLines+10) + "\n",
		"wide.txt":  wide,
		"huge.txt":  strings.Repeat("x", maxBytes+1) + "\nshort\n",
		"program":   "\x7fELF\x00\x01",
		"sub/f.txt": "nested",
	})
	tool := New(tooltest.Local(t, dir))

	tests := []struct {
		name    string
		input   map[string]any
		want    string
		wantErr string
	}{
		{
			name:  "whole file",
			input: map[string]any{"path": "three.txt"},
			want:  "one\ntwo\nthree",
		},
		{
			name:  "absolute path",
			input: map[string]any{"path": filepath.Join(dir, "sub", "f.txt")},
			want:  "nested",
		},
		{
			name:  "empty file",
			input: map[string]any{"path": "empty.txt"},
			want:  "(empty file)",
		},
		{
			name:  "offset",
			input: map[string]any{"path": "three.txt", "offset": 2},
			want:  "two\nthree",
		},
		{
			name:  "offset below one starts at the first line",
			input: map[string]any{"path": "three.txt", "offset": 0},
			want:  "one\ntwo\nthree",
		},
		{
			name:  "limit below one has no limit",
			input: map[string]any{"path": "three.txt", "limit": -1},
			want:  "one\ntwo\nthree",
		},
		{
			name:  "limit with more lines",
			input: map[string]any{"path": "three.txt", "limit": 1},
			want:  "one\n\n[2 more lines in file. Use offset=2 to continue.]",
		},
		{
			name:  "line limit",
			input: map[string]any{"path": "long.txt"},
			want:  numbered(1, maxLines) + "\n\n[Showing lines 1-2000 of 2010. Use offset=2001 to continue.]",
		},
		{
			name:  "line limit from an offset",
			input: map[string]any{"path": "long.txt", "offset": 2001},
			want:  numbered(2001, 2010),
		},
		{
			name:  "byte limit",
			input: map[string]any{"path": "wide.txt"},
			want:  strings.Repeat(wideLine+"\n", 51) + "\n[Showing lines 1-51 of 60 (50KB limit). Use offset=52 to continue.]",
		},
		{
			name:  "first line over the byte limit",
			input: map[string]any{"path": "huge.txt"},
			want:  "[Line 1 is 51201 bytes, over the 50KB limit. Use bash: sed -n '1p' 'huge.txt' | head -c 51200. The next line is offset 2.]",
		},
		{
			name:    "offset past the end",
			input:   map[string]any{"path": "three.txt", "offset": 9},
			wantErr: "offset 9 is past the end of the file (3 lines)",
		},
		{
			name:    "binary file",
			input:   map[string]any{"path": "program"},
			wantErr: "binary file",
		},
		{
			name:    "missing file",
			input:   map[string]any{"path": "missing.txt"},
			wantErr: "missing.txt",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, isErr := tooltest.Call(t, tool, tt.input)

			if tt.wantErr != "" {
				require.True(t, isErr, got)
				assert.Contains(t, got, tt.wantErr)
				return
			}
			require.False(t, isErr, got)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestReadMedia(t *testing.T) {
	png := "\x89PNG\r\n\x1a\n\x00\x00\x00\x0dIHDR"
	pdf := "%PDF-1.7\n"

	dir := t.TempDir()
	tooltest.WriteFiles(t, dir, map[string]string{
		"pixel.png": png,
		"doc.pdf":   pdf,
		"named.txt": png,
	})
	tool := New(tooltest.Local(t, dir))

	tests := []struct {
		name string
		path string
		want []ai.Content
	}{
		{
			name: "image",
			path: "pixel.png",
			want: []ai.Content{
				ai.Text{Text: "Read image/png file"},
				ai.File{MediaType: "image/png", Data: []byte(png)},
			},
		},
		{
			name: "pdf",
			path: "doc.pdf",
			want: []ai.Content{
				ai.Text{Text: "Read application/pdf file"},
				ai.File{MediaType: "application/pdf", Data: []byte(pdf)},
			},
		},
		{
			name: "the content decides, not the name",
			path: "named.txt",
			want: []ai.Content{
				ai.Text{Text: "Read image/png file"},
				ai.File{MediaType: "image/png", Data: []byte(png)},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := tooltest.Result(t, tool, map[string]any{"path": tt.path})

			require.False(t, res.IsError)
			assert.Equal(t, tt.want, res.Content)
		})
	}
}

func TestReadRejectsALargeImage(t *testing.T) {
	dir := t.TempDir()
	big := "\x89PNG\r\n\x1a\n" + strings.Repeat("x", maxImageBytes)
	tooltest.WriteFiles(t, dir, map[string]string{"big.png": big})

	got, isErr := tooltest.Call(t, New(tooltest.Local(t, dir)), map[string]any{"path": "big.png"})

	require.True(t, isErr)
	assert.Contains(t, got, "too large")
}

func TestReadRejectsALargeFileBeforeItReadsIt(t *testing.T) {
	dir := t.TempDir()
	// A file with no data written is sparse, so it takes no disk space.
	file, err := os.Create(filepath.Join(dir, "big file.log"))
	require.NoError(t, err)
	require.NoError(t, file.Truncate(maxFileBytes+1))
	require.NoError(t, file.Close())

	got, isErr := tooltest.Call(t, New(tooltest.Local(t, dir)), map[string]any{"path": "big file.log"})

	require.True(t, isErr)
	assert.Contains(t, got, "big file.log is too large")
	assert.Contains(t, got, "sed -n '1,100p' 'big file.log'")
}

func TestReadRejectsAHiddenPath(t *testing.T) {
	dir := t.TempDir()
	tooltest.WriteFiles(t, dir, map[string]string{"secret/key": "k"})

	got, isErr := tooltest.Call(t, New(tooltest.Hidden(t, dir)), map[string]any{"path": "secret/key"})

	require.True(t, isErr)
	assert.Contains(t, got, "hidden")
}
