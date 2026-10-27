package tool

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/sonnes/gopi/pkg/ai"
)

// ErrUnknownTool reports a tool name that a list of tools does not
// hold, or a tool that a driver cannot give its agent.
var ErrUnknownTool = errors.New("tool: unknown tool")

// Named returns a tool with a name and no run function. It stands for
// a tool that only the agent of a CLI runs, such as apply_patch of
// Codex. A driver of a CLI maps the name to its own tool, and the
// harness skips it. Run fails with [errors.ErrUnsupported].
func Named(name string) Tool {
	return named{name: name}
}

// IsNamed reports whether t came from [Named].
func IsNamed(t Tool) bool {
	_, ok := t.(named)
	return ok
}

type named struct {
	name string
}

func (n named) Info() ai.ToolInfo { return ai.ToolInfo{Name: n.name} }

func (n named) Run(context.Context, ai.ToolCall) (ai.ToolResult, error) {
	return ai.ToolResult{}, fmt.Errorf("tool %q: only the agent of a CLI runs it: %w", n.name, errors.ErrUnsupported)
}

// Select returns the tools of all that names keeps, in the order of
// all. [ListNames] reads names, so an entry can be a gopi name or a
// Claude Code name, and can have content. If names is nil, Select
// returns all. An MCP name passes, because the MCP servers of an agent
// give their tools outside all. Any other entry that names no tool of
// all fails with [ErrUnknownTool], so a typo does not remove a tool
// without a word.
func Select(all []Tool, names []string) ([]Tool, error) {
	if names == nil {
		return all, nil
	}
	have := make([]string, len(all))
	for i, t := range all {
		have[i] = t.Info().Name
	}
	for _, entry := range names {
		name, _ := SplitRule(entry)
		if strings.HasPrefix(name, MCPPrefix) {
			continue
		}
		known := slices.Contains(have, name) || slices.Contains(have, Canonical(name))
		if !known {
			return nil, fmt.Errorf("%w: %q is not in the tool list", ErrUnknownTool, name)
		}
	}

	keep := ListNames(names)
	var out []Tool
	for _, t := range all {
		if slices.Contains(keep, t.Info().Name) {
			out = append(out, t)
		}
	}
	return out, nil
}
