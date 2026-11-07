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
// [Thinking], Mode to [PermissionMode], Limits to [Limits], MCP to
// [MCP], Idle to [Idle], Wake to [Wake], and Approve.
//
// The driver gives the CLI each Deny rule with no content, such as
// "web_fetch", with [DisallowedTools]. It gives the CLI no rule with
// content. The CLI reads such a rule in its own way, so the hooks of
// the caller check it, as the host does.
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
// Close. A process that dies leaves its folder until the next start. The
// folder of each session is in the folder gopi-defs of [os.TempDir]. The
// CLI lists each skill and loads its body, so the driver adds no text for
// Skills. If the first text of a user turn is a slash call of a skill,
// the driver moves that text after the files of the turn, because the
// CLI loads a skill only from the last text block. A name of Skills that Skillset and Dirs do not have fails the
// start, as in the harness. A skill name with a slash also fails the
// start, because the name is a folder name. The driver does not
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
	own := newConfig(d.opts)
	defs := clidefs.New(c.Skillset, c.Dirs, c.SessionID, c.Spec)
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
	if len(c.AddDirs) > 0 {
		opts = append(opts, AddDirs(c.AddDirs...))
	}
	if c.Sections != nil {
		opts = append(opts, Sections(c.Sections))
	}
	if c.AutoCompact > 0 {
		opts = append(opts, AutoCompact(c.AutoCompact))
	}
	if c.Effort != "" {
		opts = append(opts, Thinking(c.Effort))
	}
	if c.Mode != "" {
		// The modes of a config have the names of the modes of the CLI.
		opts = append(opts, PermissionMode(Permission(c.Mode)))
	}
	if c.Limits != (agent.Limits{}) {
		opts = append(opts, Limits(c.Limits))
	}
	if removed := wholeTools(c.Deny); len(removed) > 0 {
		opts = append(opts, DisallowedTools(removed...))
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

// wholeTools returns the rules of rules with no content. Each one
// names a whole tool, such as "web_fetch".
func wholeTools(rules []string) []string {
	var out []string
	for _, r := range rules {
		_, content := tool.SplitRule(r)
		if content == "" {
			out = append(out, r)
		}
	}
	return out
}
