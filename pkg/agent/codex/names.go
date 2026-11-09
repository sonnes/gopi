package codex

import (
	"fmt"
	"slices"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/tool"
)

// codexTools are the gopi names of the tools of the CLI that a hook at
// PreToolUse sees, so that the agent can remove them. The CLI also
// has web_search and agent, but its hooks do not see them, so a Deny
// rule cannot remove them, and the agent keeps them.
var codexTools = []string{
	tool.Bash,       // commandExecution
	tool.ApplyPatch, // fileChange
}

// Tools returns a [tool.Named] tool for each tool of the CLI: bash,
// apply_patch, web_search, and agent, for a tool list that gives a
// session each tool of the CLI.
func Tools() []tool.Tool {
	return []tool.Tool{
		tool.Named(tool.Bash),
		tool.Named(tool.ApplyPatch),
		tool.Named(tool.WebSearch),
		tool.Named(tool.Agent),
	}
}

// leftOutTools returns the tools of the CLI that c does not give the
// agent: each tool of [codexTools] that is not in the Tools of c, or
// that ToolNames does not keep. The CLI has no flag that removes a
// tool, so the agent denies each of them in a hook. A well-known tool
// that the CLI does not have, such as read_file, is skipped, and so is
// a tool of the caller, because the CLI cannot run its code. A tool of
// the caller that ToolNames keeps fails with [tool.ErrUnknownTool]. A
// [tool.Named] tool that the registry does not hold names a tool of
// another CLI, so it is skipped.
func leftOutTools(c agent.Config) ([]string, error) {
	kept, err := tool.Select(c.Tools, c.ToolNames)
	if err != nil {
		return nil, err
	}
	var have []string
	for _, t := range kept {
		name := t.Info().Name
		_, ok := tool.Lookup(name)
		if !ok && !tool.IsNamed(t) && c.ToolNames != nil {
			return nil, fmt.Errorf("%w: %q is not a tool of codex", tool.ErrUnknownTool, name)
		}
		have = append(have, name)
	}
	var out []string
	for _, name := range codexTools {
		if !slices.Contains(have, name) {
			out = append(out, name)
		}
	}
	return out, nil
}
