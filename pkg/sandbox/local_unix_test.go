//go:build unix

package sandbox

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/sandbox/fence"
)

func TestLocalReadFileRefusesAFileThatIsNotRegular(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, syscall.Mkfifo(filepath.Join(dir, "fifo"), 0o644))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "sub"), 0o755))
	box := newLocal(t, dir)

	for _, path := range []string{"fifo", "sub"} {
		t.Run(path, func(t *testing.T) {
			_, err := box.ReadFile(t.Context(), path)

			require.ErrorContains(t, err, "not a regular file")
		})
	}
}

func TestLocalPolicyDoesNotWriteThroughALink(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	outside := filepath.Join(parent, "outside")
	require.NoError(t, os.Mkdir(root, 0o755))
	require.NoError(t, os.Mkdir(outside, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "old.txt"), []byte("old"), 0o644))
	require.NoError(t, os.Symlink(filepath.Join(outside, "new.txt"), filepath.Join(root, "dangling")))
	require.NoError(t, os.Link(filepath.Join(outside, "old.txt"), filepath.Join(root, "hardlink")))
	box := newLocal(t, root, Policy(fence.Write(root)))

	for _, path := range []string{"dangling", "hardlink"} {
		t.Run(path, func(t *testing.T) {
			err := box.WriteFile(t.Context(), path, []byte("x"))

			require.Error(t, err)
		})
	}
	assert.NoFileExists(t, filepath.Join(outside, "new.txt"))
	assert.Equal(t, "old", readFile(t, outside, "old.txt"))
}

func TestLocalExecReportsASignal(t *testing.T) {
	box := newLocal(t, t.TempDir())

	res, err := box.Exec(t.Context(), Command{Line: "kill -9 $$"})

	require.NoError(t, err)
	assert.Equal(t, 128+int(syscall.SIGKILL), res.ExitCode)
}
