package claude

import (
	"fmt"
	"slices"
	"strings"

	"github.com/sonnes/gopi/pkg/tool"
)

// claudeName returns the Claude Code name of a tool name, from the
// registry of [tool.Lookup]. The agent reads the names of the CLI with
// [tool.Canonical], and writes them with claudeName. Each other name
// stays the same, so an MCP name and a tool of the CLI that the
// registry does not hold, such as DesignSync, pass through.
func claudeName(name string) string {
	k, ok := tool.Lookup(name)
	if ok && k.ClaudeName() != "" {
		return k.ClaudeName()
	}
	return name
}

// claudeRules returns the permission rules with Claude Code names. The
// content of each rule stays the same, and a prefix rule stays the
// same.
func claudeRules(rules []string) []string {
	out := make([]string, 0, len(rules))
	for _, r := range rules {
		name, content := tool.SplitRule(r)
		if strings.HasSuffix(name, "*") {
			out = append(out, r)
			continue
		}
		out = append(out, claudeName(name)+content)
	}
	return out
}

// builtinTools returns the Claude Code names of the tools of names that
// are not MCP tools, for the --tools flag. The flag lists only the
// built-in tools of the CLI, so it skips a well-known tool that Claude
// Code does not have, such as apply_patch.
func builtinTools(names []string) []string {
	out := []string{}
	for _, n := range names {
		name, _ := tool.SplitRule(n)
		if strings.HasPrefix(name, tool.MCPPrefix) {
			continue
		}
		k, known := tool.Lookup(name)
		if known && k.ClaudeName() == "" {
			continue
		}
		c := claudeName(name)
		if !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	return out
}

// cliTools returns the gopi name of each tool of all that names keeps
// and that Claude Code has, then tool_search, for --tools. The CLI
// loads the deferred MCP tools with ToolSearch, so it is always on. A
// well-known tool that Claude Code does not have, such as apply_patch,
// is skipped, and so is a tool of the caller, because the CLI cannot
// run its code. A tool of the caller that names keeps fails with
// [tool.ErrUnknownTool]. A [tool.Named]
// tool that the registry does not hold, such as DesignSync, keeps its
// name, because it names a tool of the CLI.
func cliTools(all []tool.Tool, names []string) ([]string, error) {
	kept, err := tool.Select(all, names)
	if err != nil {
		return nil, err
	}
	var out []string
	add := func(name string) {
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	for _, t := range kept {
		name := t.Info().Name
		k, ok := tool.Lookup(name)
		switch {
		case ok && k.ClaudeName() != "":
			add(k.Name)
		case ok:
			// A well-known tool that Claude Code does not have.
		case tool.IsNamed(t):
			// A tool of the CLI that the registry does not hold keeps
			// its name.
			add(name)
		case names != nil:
			return nil, fmt.Errorf("%w: %q is not a tool of claude-code", tool.ErrUnknownTool, name)
		}
	}
	add(tool.ToolSearch)
	return out, nil
}

// Tools returns a [tool.Named] tool for each well-known tool that Claude
// Code has, for a tool list that gives a session each tool of the CLI.
func Tools() []tool.Tool {
	var out []tool.Tool
	for _, k := range tool.Registry() {
		if k.ClaudeName() != "" {
			out = append(out, tool.Named(k.Name))
		}
	}
	return out
}
