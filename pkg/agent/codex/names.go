package codex

import "github.com/sonnes/gopi/pkg/tool"

// Tools returns a [tool.Named] tool for each tool of the CLI: bash,
// apply_patch, web_search, and agent. A caller adds them to a tool
// list, so that a subset of the list can name them. The agent ignores
// the tools of its config, and the CLI runs each of its tools.
func Tools() []tool.Tool {
	return []tool.Tool{
		tool.Named(tool.Bash),
		tool.Named(tool.ApplyPatch),
		tool.Named(tool.WebSearch),
		tool.Named(tool.Agent),
	}
}
