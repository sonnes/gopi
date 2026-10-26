package codex

import (
	"context"
	"errors"
	"fmt"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
)

// Driver returns an [agent.Driver] that starts a Codex agent for each
// session. The options of the driver come first, and the fields of the
// config come after them.
//
// The driver maps Model to the ID of the model, Dir, System, Effort to
// [Thinking], MCP to [MCP], and Approve. With an Approve function, the
// policy is [ApprovalUntrusted] unless an option of the driver sets
// [Policy], so that the CLI asks before a command that is not known to
// be safe. The mode replaces the policy and the sandbox of the options:
//
//   - The plan mode is [SandboxReadOnly] with [ApprovalNever]. A write
//     fails in the sandbox.
//   - The default mode is [SandboxReadOnly] with [ApprovalUntrusted]. A
//     command that is not known to be safe asks, and so does each file
//     change, because the sandbox does not let it write.
//   - The accept edits mode is [SandboxWorkspaceWrite] with
//     [ApprovalOnRequest]. A file change and a command in the working
//     directory run with no question. A command that needs more asks.
//     So the mode asks less than the accept edits mode of Claude Code.
//   - The bypass mode is [SandboxDangerFullAccess] with [ApprovalNever].
//
// The driver rejects Skills and Tools, because the CLI loads its own
// skills and runs only its own tools. It skips Dirs: the caller gives
// the CLI its skills and agents. It rejects Allow, Ask, and Deny,
// because Codex has no rules for each tool. It runs the hooks at the
// points of [Hooks]. Start rejects a hook at another point, from the
// config or from an option.
func Driver(opts ...Option) agent.Driver {
	return driver{opts: opts}
}

type driver struct {
	opts []Option
}

// ListModels starts the app server, and lists the models that the CLI
// serves to its login with model/list. The options of the driver choose
// the binary, the folder, and the environment. The list skips a hidden
// model, and puts the default model first. A catalog calls it from
// FetchDriverModels.
func (d driver) ListModels(ctx context.Context) ([]ai.Model, error) {
	return listModels(ctx, newConfig(d.opts))
}

// Start starts the durable agent of the session c.Session.
func (d driver) Start(ctx context.Context, c agent.Config) (agent.Runner, error) {
	rejected := []struct {
		field string
		set   bool
	}{
		{"Skills", len(c.Skills) > 0},
		{"Tools", len(c.Tools) > 0},
		{"Allow", len(c.Allow) > 0},
		{"Ask", len(c.Ask) > 0},
		{"Deny", len(c.Deny) > 0},
	}
	for _, r := range rejected {
		if r.set {
			return nil, unsupported(r.field)
		}
	}
	// The hooks of the options come before the hooks of the config, as
	// in the agent.
	hooks := append(newConfig(d.opts).hooks, c.Hooks...)
	if err := agent.CheckHooks(hooks, hookPoints...); err != nil {
		return nil, fmt.Errorf("codex: %w", err)
	}
	if err := agent.CheckMode(c.Mode, agent.AllModes...); err != nil {
		return nil, fmt.Errorf("codex: %w", err)
	}

	var opts []Option
	if c.Approve != nil {
		opts = append(opts, Policy(ApprovalUntrusted))
	}
	opts = append(opts, d.opts...)
	if c.Store != nil {
		opts = append(opts, Store(c.Store))
	}
	if c.Model.ID != "" {
		opts = append(opts, Model(c.Model.ID))
	}
	if c.Dir != "" {
		opts = append(opts, Dir(c.Dir))
	}
	if c.Sections != nil {
		opts = append(opts, Sections(c.Sections))
	}
	if c.Effort != "" {
		opts = append(opts, Thinking(c.Effort))
	}
	opts = append(opts, modeOptions(c.Mode)...)
	if len(c.MCP) > 0 {
		opts = append(opts, MCP(c.MCP...))
	}
	if c.Approve != nil {
		opts = append(opts, Approve(c.Approve))
	}
	if len(c.Hooks) > 0 {
		opts = append(opts, Hooks(c.Hooks...))
	}
	return Durable(ctx, c.Session, opts...)
}

// modeOptions returns the policy and the sandbox of the mode m. The doc
// of [Driver] gives the reason for each pair.
func modeOptions(m agent.Mode) []Option {
	switch m {
	case agent.ModePlan:
		return []Option{
			Policy(ApprovalNever),
			Sandbox(SandboxReadOnly),
		}
	case agent.ModeDefault:
		return []Option{
			Policy(ApprovalUntrusted),
			Sandbox(SandboxReadOnly),
		}
	case agent.ModeAcceptEdits:
		return []Option{
			Policy(ApprovalOnRequest),
			Sandbox(SandboxWorkspaceWrite),
		}
	case agent.ModeBypass:
		return []Option{
			Policy(ApprovalNever),
			Sandbox(SandboxDangerFullAccess),
		}
	default:
		return nil
	}
}

// unsupported returns the error of a config field that the driver
// cannot honor.
func unsupported(field string) error {
	return fmt.Errorf("codex: the config field %s: %w", field, errors.ErrUnsupported)
}
