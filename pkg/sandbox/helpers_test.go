package sandbox

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeFiles creates each file under dir. A key is a slash-separated
// path, and a value is the content of the file.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()

	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}
}

// readFile returns the content of a file under dir.
func readFile(t *testing.T, dir, name string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	require.NoError(t, err)
	return string(data)
}

// newLocal returns a local sandbox on dir. If an option sets a policy
// and this OS has no fence, it skips the test.
func newLocal(t *testing.T, dir string, opts ...Option) Sandbox {
	t.Helper()

	box, err := Local(dir, opts...)
	if errors.Is(err, errors.ErrUnsupported) {
		t.Skip("no fence on this OS")
	}
	require.NoError(t, err)
	return box
}
