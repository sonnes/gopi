package grep

import (
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/sandbox/internal/tooltest"
)

func TestGrep(t *testing.T) {
	dir := t.TempDir()
	tooltest.WriteFiles(t, dir, tooltest.SearchFiles)

	tests := []struct {
		name    string
		input   map[string]any
		want    string
		wantErr string
	}{
		{
			name:  "matches with file and line",
			input: map[string]any{"pattern": "Hello"},
			want:  "a.go:3:func Hello() {}\nb.txt:1:Hello World\nsub/deep/e.md:1:# Hello",
		},
		{
			name:  "ignore case",
			input: map[string]any{"pattern": "hello", "ignore_case": true},
			want: "a.go:3:func Hello() {}\nb.txt:1:Hello World\nb.txt:3:hello again\n" +
				"sub/deep/e.md:1:# Hello",
		},
		{
			name:  "glob on the file name",
			input: map[string]any{"pattern": "func", "glob": "*.go"},
			want:  "a.go:3:func Hello() {}\nsub/c.go:3:func Nested() {}",
		},
		{
			name:  "glob with a directory",
			input: map[string]any{"pattern": "func", "glob": "sub/*.go"},
			want:  "sub/c.go:3:func Nested() {}",
		},
		{
			name:  "glob that matches hidden names",
			input: map[string]any{"pattern": "Hello", "glob": "*"},
			want:  "a.go:3:func Hello() {}\nb.txt:1:Hello World\nsub/deep/e.md:1:# Hello",
		},
		{
			name:  "path to a directory",
			input: map[string]any{"pattern": "package", "path": "sub"},
			want:  "sub/c.go:1:package sub",
		},
		{
			name:  "path that is not clean",
			input: map[string]any{"pattern": "package", "path": "./sub/"},
			want:  "sub/c.go:1:package sub",
		},
		{
			name:  "path to a file",
			input: map[string]any{"pattern": "line", "path": "b.txt"},
			want:  "b.txt:2:second line",
		},
		{
			name:  "glob with a directory from the path",
			input: map[string]any{"pattern": "Hello", "path": "sub", "glob": "deep/*.md"},
			want:  "sub/deep/e.md:1:# Hello",
		},
		{
			name:  "limit",
			input: map[string]any{"pattern": "Hello", "limit": 2},
			want:  "a.go:3:func Hello() {}\nb.txt:1:Hello World\n... (results truncated to 2)",
		},
		{
			name:  "no match",
			input: map[string]any{"pattern": "zzz"},
			want:  "No matches found",
		},
		{
			name:    "invalid regex",
			input:   map[string]any{"pattern": "[unclosed"},
			wantErr: "regex",
		},
		{
			name:    "missing path",
			input:   map[string]any{"pattern": "x", "path": "missing"},
			wantErr: "missing",
		},
	}
	for name, box := range tooltest.SearchBoxes(t, dir) {
		for _, tt := range tests {
			t.Run(name+"/"+tt.name, func(t *testing.T) {
				got, isErr := tooltest.Call(t, New(box), tt.input)

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
}

func TestGrepCutsLongLines(t *testing.T) {
	dir := t.TempDir()
	long := "x" + strings.Repeat("y", 2*grepMaxColumns)
	tooltest.WriteFiles(t, dir, map[string]string{"min.js": long + "\n"})

	for name, box := range tooltest.SearchBoxes(t, dir) {
		t.Run(name, func(t *testing.T) {
			got, isErr := tooltest.Call(t, New(box), map[string]any{"pattern": "x"})

			require.False(t, isErr, got)
			assert.Equal(t, "min.js:1:"+long[:grepMaxColumns]+grepLongLine, got)
		})
	}
}

func TestGrepKeepsMatchesWhenAFileCannotBeRead(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("rg is not installed")
	}
	dir := t.TempDir()
	tooltest.WriteFiles(t, dir, map[string]string{"a.txt": "Hello\n", "b.txt": "Hello\n"})
	require.NoError(t, os.Chmod(filepath.Join(dir, "b.txt"), 0o000))
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, "b.txt"), 0o644) })

	got, isErr := tooltest.Call(t, New(tooltest.Local(t, dir)), map[string]any{"pattern": "Hello"})

	require.False(t, isErr, got)
	assert.True(t, strings.HasPrefix(got, "a.txt:1:Hello\n... (rg could not search some files: "), got)
}

func TestGrepAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	tooltest.WriteFiles(t, dir, tooltest.SearchFiles)
	sub := filepath.Join(dir, "sub")

	for name, box := range tooltest.SearchBoxes(t, dir) {
		t.Run(name, func(t *testing.T) {
			got, isErr := tooltest.Call(t, New(box), map[string]any{"pattern": "package", "path": sub})

			require.False(t, isErr, got)
			assert.Equal(t, filepath.Join(sub, "c.go")+":1:package sub", got)
		})
	}
}

func TestGrepNeedsRGInARemoteSandbox(t *testing.T) {
	got, isErr := tooltest.Call(t, New(tooltest.Bare{}), map[string]any{"pattern": "x"})

	require.True(t, isErr)
	assert.Contains(t, got, "rg is not installed")
}

func TestGrepSkipsASymlinkOutOfTheSandbox(t *testing.T) {
	dir := tooltest.SymlinkOut(t)

	for name, box := range tooltest.SearchBoxes(t, dir) {
		t.Run(name, func(t *testing.T) {
			got, isErr := tooltest.Call(t, New(box), map[string]any{"pattern": "Hello"})

			require.False(t, isErr, got)
			assert.Equal(t, "a.txt:1:Hello", got)
		})
	}
}

func TestSchemaFields(t *testing.T) {
	props := New(tooltest.Local(t, t.TempDir())).Info().InputSchema.Properties

	assert.ElementsMatch(t, []string{"pattern", "path", "glob", "ignore_case", "limit"}, slices.Collect(maps.Keys(props)))
}

func TestGrepSkipsAHiddenPath(t *testing.T) {
	dir := t.TempDir()
	tooltest.WriteFiles(t, dir, map[string]string{"a.txt": "Hello\n", "secret/key": "Hello\n"})

	for name, box := range tooltest.SearchBoxes(t, dir, tooltest.HiddenPolicy(dir)) {
		t.Run(name, func(t *testing.T) {
			got, isErr := tooltest.Call(t, New(box), map[string]any{"pattern": "Hello"})

			require.False(t, isErr, got)
			assert.Equal(t, "a.txt:1:Hello", got)
		})
	}
}

func TestGrepObeysGitignoreWithAGlobAndRG(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	require.NoError(t, exec.Command("git", "init", "-q", dir).Run())
	tooltest.WriteFiles(t, dir, map[string]string{
		".gitignore":          "node_modules/\n",
		"src/b.js":            "Hello\n",
		"node_modules/x/a.js": "Hello\n",
	})
	box, ok := tooltest.SearchBoxes(t, dir)["rg"]
	if !ok {
		t.Skip("rg is not installed")
	}

	for _, glob := range []string{"*", "*.js", "src/*.js"} {
		t.Run(glob, func(t *testing.T) {
			got, isErr := tooltest.Call(t, New(box), map[string]any{"pattern": "Hello", "glob": glob})

			require.False(t, isErr, got)
			assert.Equal(t, "src/b.js:1:Hello", got)
		})
	}
}

func TestGrepSkipsADirectoryThatItCannotRead(t *testing.T) {
	dir := t.TempDir()
	tooltest.WriteFiles(t, dir, map[string]string{"a.txt": "Hello\n", "locked/b.txt": "Hello\n"})
	locked := filepath.Join(dir, "locked")
	require.NoError(t, os.Chmod(locked, 0o000))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	for name, box := range tooltest.SearchBoxes(t, dir) {
		t.Run(name, func(t *testing.T) {
			got, isErr := tooltest.Call(t, New(box), map[string]any{"pattern": "Hello"})

			require.False(t, isErr, got)
			assert.Contains(t, got, "a.txt:1:Hello")
		})
	}
}

func TestGrepSkipsABinaryFile(t *testing.T) {
	dir := t.TempDir()
	tooltest.WriteFiles(t, dir, map[string]string{"a.txt": "Hello\n", "b.bin": "Hello\x00\x01\n"})

	for name, box := range tooltest.SearchBoxes(t, dir) {
		t.Run(name, func(t *testing.T) {
			got, isErr := tooltest.Call(t, New(box), map[string]any{"pattern": "Hello"})

			require.False(t, isErr, got)
			assert.NotContains(t, got, "b.bin:1")
		})
	}
}
