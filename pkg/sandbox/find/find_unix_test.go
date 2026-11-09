//go:build unix

package find

import (
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/sandbox/internal/tooltest"
)

func TestFindSkipsAFIFO(t *testing.T) {
	dir := t.TempDir()
	tooltest.WriteFiles(t, dir, map[string]string{"a.txt": "Hello\n"})
	require.NoError(t, syscall.Mkfifo(filepath.Join(dir, "b.txt"), 0o644))

	for name, box := range tooltest.SearchBoxes(t, dir) {
		t.Run(name, func(t *testing.T) {
			got, isErr := tooltest.Call(t, New(box), map[string]any{"pattern": "*.txt"})

			require.False(t, isErr, got)
			assert.Equal(t, "a.txt", got)
		})
	}
}
