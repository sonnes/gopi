package codex

import (
	"context"
	"fmt"
	"slices"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/internal/clidefs"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/harness/permission"
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
// Codex has no rules for each tool, so the driver applies the rules of
// the config itself, as the harness reads them:
//
//   - An Allow rule accepts an approval request of the CLI that it
//     matches. The approve function does not get the request.
//   - A Deny rule blocks a call in a hook of the CLI at PreToolUse. An
//     Ask rule asks the approve function in that hook.
//   - bash or apply_patch gets a Deny rule if Tools or ToolNames leaves
//     it out. The hooks of the CLI do not see web_search and agent, so
//     the agent keeps them. ToolNames also blocks a call of each other
//     tool, such as an MCP tool, in that hook. The model still sees the
//     tool.
//
// The driver skips a well-known tool of Tools that Codex does not have,
// such as read_file, and a tool of the caller, because the CLI runs
// only its own tools. A tool of the caller that ToolNames names fails
// the start.
//
// A Deny rule, an Ask rule, a left-out tool, or ToolNames needs the
// hook command of [HookCommand]. An Allow rule needs no hook.
//
// The driver merges Skillset and Dirs, as the harness does, and
// writes them to a folder for each session at each start of the CLI, so
// an edit reaches the next process. It removes the folder at Close.
// [DefsRoot] picks the place. It gives the skills to the CLI with
// skills/extraRoots/set, and each agent as a role in the config of the
// thread. A CLI with no skills/extraRoots/set fails with
// [errors.ErrUnsupported]. A role has no key for the Tools, the
// Disallowed, or the MaxTurns of an agent, so a role runs with each tool
// of the session. The CLI lists each skill and loads its body, so the
// driver adds no text for Skills. A name of Skills that no dir has fails
// the start, as in the harness. The driver does not read the AGENTS.md
// of Dirs. The CLI also keeps the skills and the agents of its own
// folders. It runs the hooks at the points of
// [Hooks]. Start rejects a hook at another point, from the config or
// from an option.
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

// Start starts the durable agent of the session c.SessionID.
func (d driver) Start(ctx context.Context, c agent.Config) (agent.Runner, error) {
	own := newConfig(d.opts)
	defs := clidefs.New(c.Skillset, c.Dirs, own.defsRoot, c.SessionID, c.Spec)
	if err := defs.Check(ctx, c.Skills); err != nil {
		return nil, fmt.Errorf("codex: %w", err)
	}
	rules, err := ruleHooks(c)
	if err != nil {
		return nil, fmt.Errorf("codex: %w", err)
	}
	configHooks := append(slices.Clip(c.Hooks), rules...)
	// The hooks of the options come before the hooks of the config, as
	// in the agent.
	hooks := append(own.hooks, configHooks...)
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
	opts = append(opts, defsOf(defs))
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
	if approve := allowFirst(c); approve != nil {
		opts = append(opts, Approve(approve))
	}
	if len(configHooks) > 0 {
		opts = append(opts, Hooks(configHooks...))
	}
	return Durable(ctx, c.SessionID, opts...)
}

// ruleHooks returns the hooks that apply the Deny rules, the Ask rules,
// and the tools of c. A tool of the CLI that c leaves out gets a Deny
// rule, as [leftOutTools] gives it. It returns no hook for a config with
// none, so that the CLI needs no hook command. It returns an error for
// a rule of c that does not parse, also for an Allow rule, and for a
// tool that the CLI does not have.
func ruleHooks(c agent.Config) ([]agent.Hook, error) {
	left, err := leftOutTools(c)
	if err != nil {
		return nil, err
	}
	policy := permission.Policy{
		// The CLI decides the calls that no rule matches.
		Mode:    permission.Bypass,
		Allow:   c.Allow,
		Ask:     c.Ask,
		Deny:    append(slices.Clip(c.Deny), left...),
		Approve: c.Approve,
		Dir:     c.Dir,
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}

	var hooks []agent.Hook
	if len(policy.Ask) > 0 || len(policy.Deny) > 0 {
		hooks = append(hooks, policy.Hook())
	}
	if c.ToolNames != nil {
		hooks = append(hooks, agent.OnlyTools(c.ToolNames))
	}
	return hooks, nil
}

// allowFirst returns the approve function of c, with the Allow rules of
// c first: a request that an Allow rule matches is accepted, and the
// approve function does not get it. Without an Allow rule, it returns
// c.Approve. Without an approve function, a request that no Allow rule
// matches is declined.
func allowFirst(c agent.Config) agent.ApproveFunc {
	if len(c.Allow) == 0 {
		return c.Approve
	}
	policy := permission.Policy{
		Allow: c.Allow,
		Dir:   c.Dir,
	}
	return func(ctx context.Context, req agent.ApprovalRequest) (agent.Decision, error) {
		if policy.Allows(req.Call) {
			return agent.Decision{Allow: true}, nil
		}
		if c.Approve == nil {
			return agent.Decision{}, nil
		}
		return c.Approve(ctx, req)
	}
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
