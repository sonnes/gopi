package agent_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
)

func TestOnlyTools(t *testing.T) {
	h := agent.OnlyTools([]string{"bash(git diff *)", "mcp__lw__threads_get", "Glob"}).(agent.BeforeToolHook)
	tests := []struct {
		name    string
		allowed bool
	}{
		{name: "bash", allowed: true},
		{name: "mcp__lw__threads_get", allowed: true},
		{name: "mcp__lw__threads_send", allowed: false},
		{name: "read_file", allowed: false},
		{name: "find", allowed: true},
		{name: "tool_search", allowed: true},
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

func TestDenyTools(t *testing.T) {
	h := agent.DenyTools([]string{"bash", "Read", "mcp__github", "deploy_*"}).(agent.BeforeToolHook)
	tests := []struct {
		name string
		rule string
	}{
		{name: "bash", rule: "bash"},
		{name: "grep", rule: "Read"},
		{name: "Read", rule: "Read"},
		{name: "mcp__github__create_issue", rule: "mcp__github"},
		{name: "deploy_prod", rule: "deploy_*"},
		{name: "apply_patch"},
		{name: "mcp__githubx__create_issue"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			call := ai.ToolCall{ID: "c1", Name: tt.name, Input: []byte(`{}`)}

			got, err := h.BeforeTool(context.Background(), agent.State{}, call)

			if tt.rule == "" {
				assert.NoError(t, err)
				assert.Equal(t, call, got)
				return
			}
			var deny *agent.DenyError
			require.ErrorAs(t, err, &deny)
			assert.Equal(t, `the rule "`+tt.rule+`" denies the tool call "`+tt.name+`"`, deny.Reason)
		})
	}
}

func TestDenyToolsIsABeforeToolHook(t *testing.T) {
	points := agent.Points(agent.DenyTools(nil))

	assert.Equal(t, []agent.Point{agent.PointBeforeTool}, points)
}

func TestMaxTurns(t *testing.T) {
	p := &script{responses: []ai.Message{
		toolCall("c1", "echo", `{"s":"x"}`),
		toolCall("c2", "echo", `{"s":"x"}`),
		text("done"),
	}}
	a := agent.New(model(p), agent.Toolset(echo), agent.Hooks(agent.MaxTurns(2)))

	_, _, err := collect(t, a.Run(context.Background(), session.Text("hi")))

	assert.ErrorContains(t, err, "more than 2 turns")
	assert.Len(t, p.received(), 2)
}
