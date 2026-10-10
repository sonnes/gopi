// Package tooltest holds the helpers that the tests of the tool packages
// share.
package tooltest

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/sandbox"
	"github.com/sonnes/gopi/pkg/sandbox/fence"
	"github.com/sonnes/gopi/pkg/sandbox/internal/host"
	"github.com/sonnes/gopi/pkg/tool"
)

// Result runs tool with input and returns its result.
func Result(t *testing.T, tool tool.Tool, input map[string]any) ai.ToolResult {
	t.Helper()

	data, err := json.Marshal(input)
	require.NoError(t, err)

	res, err := tool.Run(t.Context(), ai.ToolCall{
		ID:    "call_1",
		Name:  tool.Info().Name,
		Input: data,
	})
	require.NoError(t, err)
	return res
}

// Call runs tool with input and returns the text of the result and
// whether the result is an error.
func Call(t *testing.T, tool tool.Tool, input map[string]any) (string, bool) {
	t.Helper()

	res := Result(t, tool, input)
	var b strings.Builder
	for _, c := range res.Content {
		text, ok := c.(ai.Text)
		require.True(t, ok, "content block %T is not text", c)
		b.WriteString(text.Text)
	}
	return b.String(), res.IsError
}

// WriteFiles creates each file under dir. A key is a slash-separated
// path, and a value is the content of the file.
func WriteFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()

	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}
}

// ReadFile returns the content of a file under dir.
func ReadFile(t *testing.T, dir, name string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	require.NoError(t, err)
	return string(data)
}

// Local returns a local sandbox on dir. If an option sets a policy and
// this OS has no fence, it skips the test.
func Local(t *testing.T, dir string, opts ...sandbox.Option) sandbox.Sandbox {
	t.Helper()

	box, err := sandbox.Local(dir, opts...)
	if errors.Is(err, errors.ErrUnsupported) {
		t.Skip("no fence on this OS")
	}
	require.NoError(t, err)
	return box
}

// HiddenPolicy is a policy that can write in dir and hides the
// directory secret under dir.
func HiddenPolicy(dir string) sandbox.Option {
	return sandbox.Policy(fence.Write(dir), fence.Hide(filepath.Join(dir, "secret")))
}

// Hidden returns a local sandbox on dir with [HiddenPolicy].
func Hidden(t *testing.T, dir string) sandbox.Sandbox {
	t.Helper()

	return Local(t, dir, HiddenPolicy(dir))
}

// hostBox is a sandbox with paths on this host, such as a local sandbox.
type hostBox interface {
	sandbox.Sandbox
	host.Pather
}

// noRG is a local sandbox whose shell has no rg. The search tools fall
// back to their own walk of the directory.
type noRG struct{ hostBox }

func (noRG) Exec(context.Context, sandbox.Command) (sandbox.Result, error) {
	return sandbox.Result{ExitCode: 127}, nil
}

// Bare is a sandbox with no rg and no host paths, like a remote sandbox
// whose image has no rg.
type Bare struct{}

func (Bare) Exec(context.Context, sandbox.Command) (sandbox.Result, error) {
	return sandbox.Result{ExitCode: 127}, nil
}
func (Bare) ReadFile(context.Context, string) ([]byte, error) { return nil, nil }
func (Bare) WriteFile(context.Context, string, []byte) error  { return nil }
func (Bare) Dir() string                                      { return "/" }

// SearchBoxes returns a local sandbox on dir for each search path: rg,
// if it is installed, and the walk.
func SearchBoxes(t *testing.T, dir string, opts ...sandbox.Option) map[string]sandbox.Sandbox {
	t.Helper()

	boxes := map[string]sandbox.Sandbox{
		"walk": noRG{Local(t, dir, opts...).(hostBox)},
	}
	if _, err := exec.LookPath("rg"); err == nil {
		boxes["rg"] = Local(t, dir, opts...)
	} else {
		t.Log("rg is not installed: only the walk is tested")
	}
	return boxes
}

// SearchFiles are the files of the search tests. Two are hidden.
var SearchFiles = map[string]string{
	"a.go":          "package main\n\nfunc Hello() {}\n",
	"b.txt":         "Hello World\nsecond line\nhello again\n",
	"sub/c.go":      "package sub\n\nfunc Nested() {}\n",
	".hidden/d.go":  "package hidden\n\nfunc Hello() {}\n",
	".dotfile":      "Hello\n",
	"sub/deep/e.md": "# Hello\n",
}

// SymlinkOut returns a directory with the file a.txt and two symlinks
// that point outside the directory: link.txt to a file, and linkdir to a
// directory. Each file holds the line Hello.
func SymlinkOut(t *testing.T) string {
	t.Helper()

	outside := t.TempDir()
	WriteFiles(t, outside, map[string]string{"secret.txt": "Hello secret\n"})
	dir := t.TempDir()
	WriteFiles(t, dir, map[string]string{"a.txt": "Hello\n"})
	require.NoError(t, os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(dir, "link.txt")))
	require.NoError(t, os.Symlink(outside, filepath.Join(dir, "linkdir")))
	return dir
}
