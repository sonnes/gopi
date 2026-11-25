package agent

import (
	"context"
	"fmt"
	"slices"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/tool"
)

// OnlyTools returns a BeforeTool hook that blocks each call of a tool
// that names leave out. A driver of a CLI adds it for
// [Config.ToolNames] when the CLI cannot hide a tool from the model,
// such as an MCP tool. The model then sees the tool, and a call of it
// fails.
//
// A name can be a Claude Code name, such as "Glob", and can have
// content, such as "bash(git diff *)". [tool.ListNames] reads the names.
// The hook always lets [tool.ToolSearch] run, because the search loads
// the deferred tools, such as the MCP tools that names keep. The loop
// keeps its search with [Config.ToolNames] too. A nil or empty list
// blocks each other call.
func OnlyTools(names []string) Hook {
	names = append(tool.ListNames(names), tool.ToolSearch)
	return BeforeTool(func(_ context.Context, _ State, c ai.ToolCall) (ai.ToolCall, error) {
		if !slices.Contains(names, c.Name) {
			return c, fmt.Errorf("the tool %q is not one of the tools of this agent", c.Name)
		}
		return c, nil
	})
}

// MaxTurns returns a BeforeCall hook that fails a run before the model
// call of turn n+1. The agent has no limit on the turns of a run, so the
// limit is a hook.
func MaxTurns(n int) Hook {
	return BeforeCall(func(_ context.Context, s State, r Request) (Request, error) {
		if s.Turn() > n {
			return r, fmt.Errorf("agent: the run used more than %d turns", n)
		}
		return r, nil
	})
}
