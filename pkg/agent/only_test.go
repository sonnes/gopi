package agent_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
)

func TestOnlyTools(t *testing.T) {
	h := agent.OnlyTools([]string{"bash(git diff *)", "mcp__lw__threads_get", "Glob"})
	tests := []struct {
		name    string
		allowed bool
	}{
		{name: "bash", allowed: true},
		{name: "mcp__lw__threads_get", allowed: true},
		{name: "mcp__lw__threads_send", allowed: false},
		{name: "read_file", allowed: false},
		{name: "find", allowed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			call := ai.ToolCall{ID: "c1", Name: tt.name}

			got, err := h.BeforeTool(context.Background(), agent.State{}, call)

			if tt.allowed {
				assert.NoError(t, err)
				assert.Equal(t, call, got)
				return
			}
			assert.EqualError(t, err, `the tool "`+tt.name+`" is not one of the tools of this agent`)
		})
	}
}

func TestOnlyToolsIsABeforeToolHook(t *testing.T) {
	points := agent.Points(agent.OnlyTools(nil))

	assert.Equal(t, []agent.Point{agent.PointBeforeTool}, points)
}
