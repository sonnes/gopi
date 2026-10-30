package claude

import (
	"context"
	"fmt"
	"slices"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/internal/clidefs"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/tool"
)

// Driver returns an [agent.Driver] that starts a Claude Code agent for
// each session. The options of the driver come first, and the fields of
// the config come after them.
//
// The driver maps Model to the ID of the model, Dir, System, Effort to
// [Thinking], Mode to [PermissionMode], Allow to [AllowedTools], Ask to
// [AskTools], Deny to [DisallowedTools], MCP to [MCP], Idle to
// [Idle], Wake to [Wake], and Approve.
//
// The driver maps the names of the Tools that ToolNames keeps to
// [OnlyTools], with ToolSearch, because the CLI loads the deferred MCP
// tools with it. It skips a well-known tool that Claude Code does not
// have, such as apply_patch, and a tool of the caller, because the CLI
// runs only its own tools. A tool of the caller that ToolNames names
// fails the start. With ToolNames, a hook blocks the MCP tools that
// ToolNames does not name.
//
// The driver merges Skillset and Dirs, as the harness does, and
// writes the skills and the agents to a folder for each session. It
// gives the folder to the CLI with --add-dir at each start of the CLI,
// so an edit reaches the next process, and it removes the folder at
// Close. [DefsRoot] picks the place. The CLI lists each skill and loads
// its body, so the driver adds no text for Skills. A name of Skills that
// no dir has fails the start, as in the harness. The driver does not
// read the AGENTS.md of Dirs. The CLI also keeps the skills and the
// agents of its own folders. A subagent gets the model ID of its spec
// only if the spec has the account of Spec. Any other model of a file
// gives the subagent the model of the parent. It runs the
// hooks at the points of [Hooks]. Start rejects a hook at a model call,
// in the options of the driver or in the config.
func Driver(opts ...Option) agent.Driver {
	return driver{opts: opts}
}

type driver struct {
	opts []Option
}

// ListModels starts the CLI with the options of the driver, and returns
// the models that the initialize response lists: each alias that
// --model takes, such as "opus", then each full ID that an alias
// resolves to. The CLI makes no model call, but it starts as for a run,
// so the hooks of its settings run, such as a SessionStart hook. A
// catalog calls it from FetchDriverModels.
func (d driver) ListModels(ctx context.Context) ([]ai.Model, error) {
	return listModels(ctx, newAgent(d.opts).cfg)
}

// Start starts the durable agent of the session c.SessionID.
func (d driver) Start(ctx context.Context, c agent.Config) (agent.Runner, error) {
	tools, err := cliTools(c.Tools, c.ToolNames)
	if err != nil {
		return nil, fmt.Errorf("claude: %w", err)
	}
	// The hooks of the driver come first, as its options do.
	var own config
	for _, opt := range d.opts {
		opt(&own)
	}
	defs := clidefs.New(c.Skillset, c.Dirs, own.defsRoot, c.SessionID, c.Spec)
	if err := defs.Check(ctx, c.Skills); err != nil {
		return nil, fmt.Errorf("claude: %w", err)
	}
	configHooks := slices.Clip(c.Hooks)
	if c.ToolNames != nil {
		// --tools hides only the built-in tools, so the hook blocks a
		// call of an MCP tool that ToolNames does not name. The CLI
		// loads the MCP tools with ToolSearch, so the hook keeps it.
		names := append(slices.Clip(c.ToolNames), tool.ToolSearch)
		configHooks = append(configHooks, agent.OnlyTools(names))
	}
	hooks := append(slices.Clip(own.hooks), configHooks...)
	if err := agent.CheckHooks(hooks, hookPoints...); err != nil {
		return nil, fmt.Errorf("claude: %w", err)
	}
	if err := agent.CheckMode(c.Mode, agent.AllModes...); err != nil {
		return nil, fmt.Errorf("claude: %w", err)
	}

	opts := append([]Option(nil), d.opts...)
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
	if c.Mode != "" {
		// The modes of a config have the names of the modes of the CLI.
		opts = append(opts, PermissionMode(Permission(c.Mode)))
	}
	if len(c.Allow) > 0 {
		opts = append(opts, AllowedTools(c.Allow...))
	}
	if len(c.Ask) > 0 {
		opts = append(opts, AskTools(c.Ask...))
	}
	if len(c.Deny) > 0 {
		opts = append(opts, DisallowedTools(c.Deny...))
	}
	// An OnlyTools option of the driver limits the list of the config.
	opts = append(opts, OnlyTools(limitTools(tools, own.only)...))
	if len(c.MCP) > 0 {
		opts = append(opts, MCP(c.MCP...))
	}
	if c.Approve != nil {
		opts = append(opts, Approve(c.Approve))
	}
	if c.Idle != nil {
		opts = append(opts, Idle(c.Idle))
	}
	if c.Wake != nil {
		opts = append(opts, Wake(c.Wake))
	}
	if len(configHooks) > 0 {
		opts = append(opts, Hooks(configHooks...))
	}
	return Durable(ctx, c.SessionID, opts...)
}
