package codex

import (
	"context"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/permission"
	"github.com/sonnes/gopi/pkg/tool"
)

// Driver returns an [agent.Driver] that starts a Codex agent for each
// session. Its Start calls [Durable] with the config of the session and
// opts.
func Driver(opts ...Option) agent.Driver {
	return driver{opts: opts}
}

type driver struct {
	opts []Option
}

// ListModels starts the app server, and lists the models that the CLI
// serves to its login with model/list. The options of the driver choose
// the binary and the environment. The list skips a hidden model, and
// puts the default model first. A catalog calls it from
// FetchDriverModels.
func (d driver) ListModels(ctx context.Context) ([]ai.Model, error) {
	return listModels(ctx, newOptions(d.opts))
}

// Start starts the durable agent of the session c.SessionID.
func (d driver) Start(ctx context.Context, c agent.Config) (agent.Runner, error) {
	a, err := Durable(ctx, c, d.opts...)
	if err != nil {
		return nil, err
	}
	return a, nil
}

// ruleHooks returns the hooks that block the tools of c: the tool of
// each Deny rule with no content, and each tool of the CLI that c
// leaves out, as [leftOutTools] gives it. It returns no hook for a
// config with none, so that the CLI needs no hook command. It returns
// an error for a Deny rule that does not parse, and for a tool that the
// CLI does not have.
func ruleHooks(c agent.Config) ([]agent.Hook, error) {
	left, err := leftOutTools(c)
	if err != nil {
		return nil, err
	}
	if err := (permission.Rules{Deny: c.Deny}).Validate(); err != nil {
		return nil, err
	}

	var hooks []agent.Hook
	denied := append(tool.WholeTools(c.Deny), left...)
	if len(denied) > 0 {
		hooks = append(hooks, agent.DenyTools(denied))
	}
	if c.ToolNames != nil {
		hooks = append(hooks, agent.OnlyTools(c.ToolNames))
	}
	return hooks, nil
}

// permissions returns the approval policy and the sandbox of the Mode
// of c. The package doc gives the reason for each pair. In
// [permission.Bypass] and with no Mode, a limit on writes or the fence
// of the Limits gives the workspace-write sandbox, so the commands
// write only in the roots.
func permissions(c agent.Config) (approvalPolicy, sandboxMode) {
	limited := c.Limits.BlockWritesOutside || c.Limits.Fence
	switch c.Mode {
	case permission.Default:
		return approvalUntrusted, sandboxReadOnly
	case permission.AcceptEdits:
		return approvalOnRequest, sandboxWorkspaceWrite
	case permission.Bypass:
		if limited {
			return approvalNever, sandboxWorkspaceWrite
		}
		return approvalNever, sandboxDangerFullAccess
	}
	policy := approvalNever
	if c.Approve != nil {
		policy = approvalUntrusted
	}
	if limited {
		return policy, sandboxWorkspaceWrite
	}
	return policy, ""
}
