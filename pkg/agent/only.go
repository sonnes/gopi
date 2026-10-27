package agent

import (
	"context"
	"fmt"
	"slices"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/tool"
)

// OnlyToolsHook blocks each tool call that is not one of its tools. A
// driver of a CLI adds it for [Config.ToolNames] when the CLI cannot
// hide a tool from the model, such as an MCP tool. The model then
// sees the tool, and a call of it fails.
type OnlyToolsHook struct {
	BaseHook
	names []string
}

// OnlyTools returns the hook that keeps only the named tools. A name
// can be a Claude Code name, such as "Glob", and can have content, such
// as "bash(git diff *)". [tool.ListNames] reads the names. A nil or
// empty list blocks each call.
func OnlyTools(names []string) OnlyToolsHook {
	return OnlyToolsHook{names: tool.ListNames(names)}
}

// BeforeTool returns an error for a call of a tool that is not one of
// the tools of h, so the call does not run.
func (h OnlyToolsHook) BeforeTool(_ context.Context, _ State, c ai.ToolCall) (ai.ToolCall, error) {
	if !slices.Contains(h.names, c.Name) {
		return c, fmt.Errorf("the tool %q is not one of the tools of this agent", c.Name)
	}
	return c, nil
}
