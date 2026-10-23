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
// The driver maps Model to the ID of the model, Dir, System, and
// Approve. With an Approve function, the policy is [ApprovalUntrusted]
// unless an option of the driver sets [Policy], so that the CLI asks
// before a command that is not known to be safe. The driver maps the
// plan mode to [SandboxReadOnly] and the bypass mode to
// [SandboxDangerFullAccess], both with [ApprovalNever]. The mode
// replaces the policy and the sandbox of the options. The driver
// rejects the default mode and the accept edits mode. The driver rejects
// Skills and Tools, because the CLI loads its own skills and runs only
// its own tools. It rejects Allow, Ask, and Deny, because Codex has no
// rules for each tool. It runs the hooks at the points of [Hooks]. Start
// rejects a hook at another point, from the config or from an option.
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
	if err := agent.CheckMode(c.Mode, modes...); err != nil {
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
	if c.System != "" {
		opts = append(opts, System(c.System))
	}
	opts = append(opts, modeOptions(c.Mode)...)
	if c.Approve != nil {
		opts = append(opts, Approve(c.Approve))
	}
	if len(c.Hooks) > 0 {
		opts = append(opts, Hooks(c.Hooks...))
	}
	return Durable(ctx, c.Session, opts...)
}

// modes are the modes that the driver honors. Codex has a policy for
// commands and a sandbox, and no rule for each tool. So it cannot ask
// before an edit and run a read, as the default mode and the accept
// edits mode do.
var modes = []agent.Mode{
	agent.ModePlan,
	agent.ModeBypass,
}

// modeOptions returns the policy and the sandbox of the mode m. The
// policy of each mode is [ApprovalNever]: the plan mode blocks a write
// in the sandbox, and the bypass mode runs each command.
func modeOptions(m agent.Mode) []Option {
	switch m {
	case agent.ModePlan:
		return []Option{
			Policy(ApprovalNever),
			Sandbox(SandboxReadOnly),
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
