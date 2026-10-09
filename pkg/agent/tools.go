package agent

import (
	"encoding/json"
	"slices"

	"github.com/sonnes/gopi/internal/transcript"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/tool"
)

// addTools adds the named tools to the tools that the model can call.
// The search tool calls it with the names of deferred tools. A name
// that the agent does not know adds nothing. The next model call
// declares the change with a system message, and the history keeps it.
func (a *Agent) addTools(names []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, name := range names {
		if a.known(name) != nil && !slices.Contains(a.state.loaded, name) {
			a.state.loaded = append(a.state.loaded, name)
		}
	}
}

// known returns the tool of Toolset named name, or nil if there is
// none. A tool is found by its name, because an [tool.Tool] can hold a
// value that == cannot compare.
func (a *Agent) known(name string) tool.Tool {
	for _, t := range a.cfg.tools {
		if t.Info().Name == name {
			return t
		}
	}
	return nil
}

// tool returns the tool named name that the model can call, or nil if
// there is none. The model can call a tool of declared that the agent
// knows. Declared is [State.Tools] at the model call.
func (a *Agent) tool(declared []ai.ToolInfo, name string) tool.Tool {
	isDeclared := slices.ContainsFunc(declared, func(info ai.ToolInfo) bool {
		return info.Name == name
	})
	if !isDeclared {
		return nil
	}
	return a.known(name)
}

// wantedTools returns the tools that the model must be able to call:
// each declared tool that the agent knows, with its info of now, then
// each tool of Toolset that is not deferred, then each tool that the
// search loaded.
func (a *Agent) wantedTools(declared []ai.ToolInfo) []ai.ToolInfo {
	var want []ai.ToolInfo
	add := func(info ai.ToolInfo) {
		has := slices.ContainsFunc(want, func(w ai.ToolInfo) bool { return w.Name == info.Name })
		if !has {
			want = append(want, info)
		}
	}

	for _, d := range declared {
		if t := a.known(d.Name); t != nil {
			add(t.Info())
		}
	}
	for _, t := range a.cfg.tools {
		if info := t.Info(); !info.Deferred {
			add(info)
		}
	}
	a.mu.Lock()
	loaded := slices.Clone(a.state.loaded)
	a.mu.Unlock()
	for _, name := range loaded {
		add(a.known(name).Info())
	}
	return want
}

// toolChange returns the blocks that change the tools of msgs to the
// tools that the model must be able to call: an [ai.ToolRemoved] for
// each tool that is gone, then an [ai.ToolAdded] for each tool that is
// new. A tool whose info changed is added again, and the addition
// replaces it. It returns nil if the tools are the same.
func (a *Agent) toolChange(msgs []ai.Message) []ai.Content {
	declared := transcript.CurrentTools(nil, msgs)
	want := a.wantedTools(declared)

	var blocks []ai.Content
	for _, d := range declared {
		gone := !slices.ContainsFunc(want, func(w ai.ToolInfo) bool { return w.Name == d.Name })
		if gone {
			blocks = append(blocks, ai.ToolRemoved{Name: d.Name})
		}
	}
	for _, w := range want {
		i := slices.IndexFunc(declared, func(d ai.ToolInfo) bool { return d.Name == w.Name })
		if i < 0 || !sameInfo(declared[i], w) {
			blocks = append(blocks, ai.ToolAdded{Tool: w})
		}
	}
	return blocks
}

// sameInfo reports if two infos tell the model the same thing. It
// compares the JSON, because an info from a saved history has a schema
// that was decoded, and the pointers differ.
func sameInfo(x, y ai.ToolInfo) bool {
	xj, xerr := json.Marshal(x)
	yj, yerr := json.Marshal(y)
	return xerr == nil && yerr == nil && string(xj) == string(yj)
}
