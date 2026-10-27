package host_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/host"
)

// TestCreateKeepsTheNamesOfTheConfig checks that the host gives the
// driver each tool name as the caller wrote it. The driver maps a
// Claude Code name.
func TestCreateKeepsTheNamesOfTheConfig(t *testing.T) {
	f := newFixture(t)

	_, err := f.h.Create(context.Background(), host.Config{
		ID:        "s1",
		Model:     "fake/m",
		ToolNames: []string{"Glob", "lookup_order"},
		Allow:     []string{"Read(src/**)", "mcp__lw__*"},
		Deny:      []string{"bash(git push *)"},
	})

	require.NoError(t, err)
	c := f.lastConfig(t)
	assert.Equal(t, []string{"Glob", "lookup_order"}, c.ToolNames)
	assert.Equal(t, []string{"Read(src/**)", "mcp__lw__*"}, c.Allow)
	assert.Equal(t, []string{"bash(git push *)"}, c.Deny)
}
