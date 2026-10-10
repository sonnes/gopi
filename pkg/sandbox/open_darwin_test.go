package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetPathReturnsThePathOfTheOpenFile(t *testing.T) {
	dir := t.TempDir()
	real, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	writeFiles(t, dir, map[string]string{"sub/f.txt": "x"})
	require.NoError(t, os.Symlink(filepath.Join(dir, "sub"), filepath.Join(dir, "link")))
	f, err := os.Open(filepath.Join(dir, "link", "f.txt"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })

	got, err := getPath(f)

	require.NoError(t, err)
	assert.Equal(t, filepath.Join(real, "sub", "f.txt"), got)
}
