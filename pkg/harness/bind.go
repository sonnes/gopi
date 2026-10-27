package harness

import (
	"context"
	"errors"
	"fmt"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/sandbox"
	"github.com/sonnes/gopi/pkg/sandbox/bash"
	"github.com/sonnes/gopi/pkg/sandbox/edit"
	"github.com/sonnes/gopi/pkg/sandbox/fence"
	"github.com/sonnes/gopi/pkg/sandbox/find"
	"github.com/sonnes/gopi/pkg/sandbox/grep"
	"github.com/sonnes/gopi/pkg/sandbox/read"
	"github.com/sonnes/gopi/pkg/sandbox/write"
	"github.com/sonnes/gopi/pkg/tool"
	"github.com/sonnes/gopi/pkg/tool/todowrite"
)

// Binder is a tool that the harness binds to each assembly before the
// first call. A tool that needs the session, such as its skills, its
// subagents, or its sandbox, is a Binder. Before Bind, Info gives the
// name of the tool, so a driver of a CLI can map the name. Run of an
// unbound tool fails.
type Binder interface {
	tool.Tool
	// Bind returns the tool for a. It returns nil if a has no use for
	// the tool, as the skill tool for an assembly with no skill.
	Bind(a *Assembly) (tool.Tool, error)
}

// SkillTool returns the skill tool. It binds to the skills of the
// assembly, and gives no tool to an assembly with no skill that the
// model can call.
func SkillTool() Binder {
	return unbound{
		name: skillToolName,
		bind: func(a *Assembly) (tool.Tool, error) {
			if t := newSkillTool(a.Skills, a.names); t != nil {
				return t, nil
			}
			return nil, nil
		},
	}
}

// AgentTool returns the agent tool. It binds to the subagents of the
// assembly, and gives no tool to an assembly with no subagent or to the
// assembly of a subagent.
func AgentTool() Binder {
	return unbound{
		name: agentToolName,
		bind: func(a *Assembly) (tool.Tool, error) {
			if !a.agentTool() {
				return nil, nil
			}
			return newAgentTool(a), nil
		},
	}
}

// Sandboxed returns a Binder that calls build with the sandbox of each
// assembly: the [Sandbox] of the harness, or a [sandbox.Local] in its
// [Dir] with the [fence.Workspace] policy. Each tool of an assembly gets
// the same sandbox. Sandboxed calls build once with a nil sandbox to
// read the info of the tool, so build must not use the sandbox before a
// call, as the tools of pkg/sandbox do not.
func Sandboxed(build func(sandbox.Sandbox) tool.Tool) Binder {
	info := build(nil).Info()
	return unbound{
		name: info.Name,
		info: info,
		bind: func(a *Assembly) (tool.Tool, error) {
			box, err := a.sandbox()
			if err != nil {
				return nil, err
			}
			return build(box), nil
		},
	}
}

// Coding returns the tools of a coding agent: the tools that read
// files, change files, and run commands in the sandbox of each session,
// each from [Sandboxed], and todo_write. Give them with [Toolset] or
// host.Tools, as Tools(Coding()...).
func Coding() []tool.Tool {
	return []tool.Tool{
		Sandboxed(read.New),
		Sandboxed(write.New),
		Sandboxed(edit.New),
		Sandboxed(bash.New),
		Sandboxed(grep.New),
		Sandboxed(find.New),
		todowrite.New(),
	}
}

// unbound is a [Binder] from a name and a bind function.
type unbound struct {
	name string
	info ai.ToolInfo
	bind func(a *Assembly) (tool.Tool, error)
}

func (u unbound) Info() ai.ToolInfo {
	if u.info.Name != "" {
		return u.info
	}
	return ai.ToolInfo{Name: u.name}
}

func (u unbound) Run(context.Context, ai.ToolCall) (ai.ToolResult, error) {
	return ai.ToolResult{}, fmt.Errorf("harness: the tool %q is not bound to a session", u.name)
}

func (u unbound) Bind(a *Assembly) (tool.Tool, error) {
	return u.bind(a)
}

// bindTools returns the tool of each tool of tools for a, in order. It
// binds each [Binder], and skips each tool of [tool.Named], because
// only the agent of a CLI runs it.
func (a *Assembly) bindTools(tools []tool.Tool) ([]tool.Tool, error) {
	var out []tool.Tool
	for _, t := range tools {
		if tool.IsNamed(t) {
			continue
		}
		b, ok := t.(Binder)
		if !ok {
			out = append(out, t)
			continue
		}
		bound, err := b.Bind(a)
		if err != nil {
			return nil, fmt.Errorf("harness: bind the tool %q: %w", t.Info().Name, err)
		}
		if bound != nil {
			out = append(out, bound)
		}
	}
	return out, nil
}

// sandbox returns the sandbox of the assembly. It makes the sandbox on
// the first call, so an assembly with no sandbox tool makes none.
func (a *Assembly) sandbox() (sandbox.Sandbox, error) {
	if a.box != nil {
		return a.box, nil
	}
	if a.h.box != nil {
		a.box = a.h.box
		return a.box, nil
	}
	box, err := sandbox.Local(a.Dir, sandbox.Policy(fence.Workspace(a.Dir)...))
	if errors.Is(err, errors.ErrUnsupported) {
		return nil, fmt.Errorf("%w: the fence of the default sandbox runs only on macOS. Give a sandbox with harness.Sandbox", err)
	}
	if err != nil {
		return nil, fmt.Errorf("the sandbox: %w", err)
	}
	a.box = box
	return box, nil
}
