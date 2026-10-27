package claude

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sonnes/gopi/pkg/tool"
)

// TestClaudeNamesRoundTrip checks that the Claude Code name that the
// agent writes to the CLI reads back as its gopi name.
func TestClaudeNamesRoundTrip(t *testing.T) {
	tests := []struct {
		name   string
		claude string
	}{
		{name: tool.Bash, claude: "Bash"},
		{name: tool.Find, claude: "Glob"},
		{name: tool.Agent, claude: "Agent"},
		{name: "Read", claude: "Read"},
		{name: "Task", claude: "Agent"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.claude, claudeName(tt.name), tt.name)
		assert.Equal(t, tool.Canonical(tt.name), tool.Canonical(claudeName(tt.name)), tt.name)
	}
}

func TestClaudeNamePassesThrough(t *testing.T) {
	for _, name := range []string{"DesignSync", "lookup_order", "mcp__lw__threads_get"} {
		assert.Equal(t, name, claudeName(name))
		assert.Equal(t, name, tool.Canonical(claudeName(name)))
	}
}

func TestBuiltinToolsSkipsAToolThatClaudeCodeDoesNotHave(t *testing.T) {
	got := builtinTools([]string{"bash", "apply_patch", "NotebookEdit", "mcp__lw__threads_get", "bash(git *)"})

	assert.Equal(t, []string{"Bash", "NotebookEdit"}, got)
}
