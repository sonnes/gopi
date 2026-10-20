package codex

import (
	"context"
	"errors"
	"fmt"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
)

// Models lists the models of the Codex CLI that a catalog registers
// with the driver. A bare model ID names the driver only if no text
// provider serves the same ID. Otherwise give the full spec, such as
// "codex/gpt-6.1-sol". A spec of the driver with another model ID
// passes the ID to the CLI.
var Models = []ai.Model{
	{ID: "gpt-6.1-sol", Name: "GPT-6.1-Sol"},
	{ID: "gpt-6-astra", Name: "GPT-6-Astra"},
	{ID: "gpt-6-sol", Name: "GPT-6-Sol"},
	{ID: "gpt-6-luna", Name: "GPT-6-Luna"},
	{ID: "gpt-5.6-sol", Name: "GPT-5.6-Sol"},
	{ID: "gpt-5.6-terra", Name: "GPT-5.6-Terra"},
	{ID: "gpt-5.6-luna", Name: "GPT-5.6-Luna"},
}

// Driver returns an [agent.Driver] that starts a Codex agent for each
// session. The options of the driver come first, and the fields of the
// config come after them.
//
// The driver maps Model to the ID of the model, Dir, System, and
// Approve. With an Approve function, the policy is [ApprovalUntrusted]
// unless an option of the driver sets [Policy], so that the CLI asks
// before a command that is not known to be safe. The driver rejects
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
	if c.Approve != nil {
		opts = append(opts, Approve(c.Approve))
	}
	if len(c.Hooks) > 0 {
		opts = append(opts, Hooks(c.Hooks...))
	}
	return Durable(ctx, c.Session, opts...)
}

// unsupported returns the error of a config field that the driver
// cannot honor.
func unsupported(field string) error {
	return fmt.Errorf("codex: the config field %s: %w", field, errors.ErrUnsupported)
}
