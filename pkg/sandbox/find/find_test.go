package find

import (
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/sandbox/internal/tooltest"
)

// ageFiles sets the modification time of each file under dir, so that
// the files sort by path when the newest comes first.
func ageFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()

	names := slices.Sorted(maps.Keys(files))
	now := time.Now()
	for i, name := range names {
		mtime := now.Add(-time.Duration(i) * time.Minute)
		require.NoError(t, os.Chtimes(filepath.Join(dir, filepath.FromSlash(name)), mtime, mtime))
	}
}

func TestFind(t *testing.T) {
	dir := t.TempDir()
	tooltest.WriteFiles(t, dir, tooltest.SearchFiles)
	ageFiles(t, dir, tooltest.SearchFiles)

	tests := []struct {
		name    string
		input   map[string]any
		want    string
		wantErr string
	}{
		{
			name:  "file name at any depth",
			input: map[string]any{"pattern": "*.go"},
			want:  "a.go\nsub/c.go",
		},
		{
			name:  "double star",
			input: map[string]any{"pattern": "**/*.md"},
			want:  "sub/deep/e.md",
		},
		{
			name:  "pattern with a directory",
			input: map[string]any{"pattern": "sub/*.go"},
			want:  "sub/c.go",
		},
		{
			name:  "path",
			input: map[string]any{"pattern": "*", "path": "sub"},
			want:  "sub/c.go\nsub/deep/e.md",
		},
		{
			name:  "pattern with a directory from the path",
			input: map[string]any{"pattern": "deep/*.md", "path": "sub"},
			want:  "sub/deep/e.md",
		},
		{
			name:  "path that is not clean",
			input: map[string]any{"pattern": "*.go", "path": "./sub/"},
			want:  "sub/c.go",
		},
		{
			name:  "limit",
			input: map[string]any{"pattern": "*", "limit": 2},
			want:  "a.go\nb.txt\n... (results truncated to 2)",
		},
		{
			name:  "no match",
			input: map[string]any{"pattern": "*.rs"},
			want:  "No files found",
		},
		{
			name:    "missing path",
			input:   map[string]any{"pattern": "*", "path": "missing"},
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

func TestFindNewestFirst(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{"old.go": "", "new.go": ""}
	tooltest.WriteFiles(t, dir, files)
	past := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(filepath.Join(dir, "old.go"), past, past))

	for name, box := range tooltest.SearchBoxes(t, dir) {
		t.Run(name, func(t *testing.T) {
			got, isErr := tooltest.Call(t, New(box), map[string]any{"pattern": "*.go"})

			require.False(t, isErr, got)
			assert.Equal(t, "new.go\nold.go", got)
		})
	}
}

func TestFindNeedsRGInARemoteSandbox(t *testing.T) {
	got, isErr := tooltest.Call(t, New(tooltest.Bare{}), map[string]any{"pattern": "*"})

	require.True(t, isErr)
	assert.Contains(t, got, "rg is not installed")
}

func TestFindSkipsASymlinkOutOfTheSandbox(t *testing.T) {
	dir := tooltest.SymlinkOut(t)

	for name, box := range tooltest.SearchBoxes(t, dir) {
		t.Run(name, func(t *testing.T) {
			got, isErr := tooltest.Call(t, New(box), map[string]any{"pattern": "*"})

			require.False(t, isErr, got)
			assert.Equal(t, "a.txt", got)
		})
	}
}

func TestFindSkipsAHiddenPath(t *testing.T) {
	dir := t.TempDir()
	tooltest.WriteFiles(t, dir, map[string]string{"a.txt": "", "secret/key": ""})

	for name, box := range tooltest.SearchBoxes(t, dir, tooltest.HiddenPolicy(dir)) {
		t.Run(name, func(t *testing.T) {
			got, isErr := tooltest.Call(t, New(box), map[string]any{"pattern": "*"})

			require.False(t, isErr, got)
			assert.Equal(t, "a.txt", got)
		})
	}
}

// gitRepo returns a git repository that ignores node_modules, with one
// file in src and one in node_modules.
func gitRepo(t *testing.T) string {
	t.Helper()
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
	return dir
}

func TestFindObeysGitignoreWithRG(t *testing.T) {
	dir := gitRepo(t)
	box, ok := tooltest.SearchBoxes(t, dir)["rg"]
	if !ok {
		t.Skip("rg is not installed")
	}

	for _, pattern := range []string{"*", "*.js", "node*"} {
		t.Run(pattern, func(t *testing.T) {
			got, isErr := tooltest.Call(t, New(box), map[string]any{"pattern": pattern})

			require.False(t, isErr, got)
			assert.NotContains(t, got, "node_modules")
		})
	}
}

func TestFindAFilePath(t *testing.T) {
	dir := t.TempDir()
	tooltest.WriteFiles(t, dir, map[string]string{"a.go": "", "b.txt": ""})

	for name, box := range tooltest.SearchBoxes(t, dir) {
		t.Run(name, func(t *testing.T) {
			got, isErr := tooltest.Call(t, New(box), map[string]any{"pattern": "*.go", "path": "a.go"})

			require.False(t, isErr, got)
			assert.Equal(t, "a.go", got)
		})
	}
}

func TestFindSkipsADirectoryThatItCannotRead(t *testing.T) {
	dir := t.TempDir()
	tooltest.WriteFiles(t, dir, map[string]string{"a.go": "", "locked/b.go": ""})
	locked := filepath.Join(dir, "locked")
	require.NoError(t, os.Chmod(locked, 0o000))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	for name, box := range tooltest.SearchBoxes(t, dir) {
		t.Run(name, func(t *testing.T) {
			got, isErr := tooltest.Call(t, New(box), map[string]any{"pattern": "*.go"})

			require.False(t, isErr, got)
			assert.Contains(t, got, "a.go")
		})
	}
}
