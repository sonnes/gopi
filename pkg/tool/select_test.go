package tool_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/tool"
)

func TestNamed(t *testing.T) {
	named := tool.Named(tool.ApplyPatch)

	assert.Equal(t, ai.ToolInfo{Name: tool.ApplyPatch}, named.Info())
	assert.True(t, tool.IsNamed(named))
	assert.False(t, tool.IsNamed(tool.Func("lookup", nil)))

	_, err := named.Run(context.Background(), ai.ToolCall{Name: tool.ApplyPatch})
	assert.ErrorIs(t, err, errors.ErrUnsupported)
}

func TestSelect(t *testing.T) {
	all := []tool.Tool{
		tool.Named(tool.Bash),
		tool.Named(tool.ReadFile),
		tool.Named(tool.Grep),
		tool.Func("lookup", nil),
	}
	tests := []struct {
		name  string
		names []string
		want  []string
	}{
		{name: "nil keeps each tool", names: nil, want: []string{"bash", "read_file", "grep", "lookup"}},
		{name: "empty keeps no tool", names: []string{}, want: nil},
		{name: "gopi names", names: []string{"grep", "bash"}, want: []string{"bash", "grep"}},
		{name: "Claude Code name", names: []string{"Read"}, want: []string{"read_file"}},
		{name: "entry with content", names: []string{"Bash(git diff *)"}, want: []string{"bash"}},
		{name: "caller tool", names: []string{"lookup"}, want: []string{"lookup"}},
		{name: "MCP name is not in the list", names: []string{"bash", "mcp__lw__threads_send"}, want: []string{"bash"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tool.Select(all, tt.names)
			require.NoError(t, err)

			var names []string
			for _, g := range got {
				names = append(names, g.Info().Name)
			}
			assert.Equal(t, tt.want, names)
		})
	}
}

func TestSelect_UnknownName(t *testing.T) {
	all := []tool.Tool{tool.Named(tool.Bash)}

	tests := []struct {
		name  string
		names []string
	}{
		{name: "well-known tool not in the list", names: []string{"Read"}},
		{name: "unknown name", names: []string{"bash", "lookup2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tool.Select(all, tt.names)
			assert.ErrorIs(t, err, tool.ErrUnknownTool)
		})
	}
}

func TestRegistryHoldsEachWellKnownTool(t *testing.T) {
	var names []string
	for _, k := range tool.Registry() {
		names = append(names, k.Name)
	}

	assert.Contains(t, names, tool.Bash)
	assert.Contains(t, names, tool.NotebookEdit)
	for _, n := range names {
		_, ok := tool.Lookup(n)
		assert.True(t, ok, n)
	}
}
