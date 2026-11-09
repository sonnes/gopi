package harness

import (
	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/catalog"
	"github.com/sonnes/gopi/pkg/def"
	"github.com/sonnes/gopi/pkg/sandbox"
	"github.com/sonnes/gopi/pkg/sandbox/fence"
)

// Option sets how a [Harness] builds each agent. The [agent.Config] of
// a call holds the settings of one session. An option that adds to a
// list, such as [Toolset], adds after the list of an earlier option.
type Option func(*Harness)

// Catalog sets the catalog that resolves the model spec. An assembly
// with no catalog fails.
func Catalog(c *catalog.Catalog) Option {
	return func(h *Harness) { h.cat = c }
}

// Defs sets the source of the instructions, the skills, and the
// subagents, such as a folder of package
// github.com/sonnes/gopi/pkg/store/fs. Each assembly loads
// [def.Layers] of src and the Defs of the config, so a definition of the
// config wins for a name. Without Defs and the Defs of the config, the
// harness reads no file, so the caller picks every folder that an agent
// reads.
//
// [DefaultPrompt] puts the instruction texts in the "instructions"
// section of the system prompt, in order, and lists the skills and the
// subagents. [SkillTool] binds to the skills, and [AgentTool] binds to
// the subagents. Each binds to nothing in an assembly with nothing to
// load, because such a tool only confuses the model.
func Defs(src def.Source) Option {
	return func(h *Harness) { h.defs = src }
}

// Toolset adds readers of the tools that the model can call. The tools
// of Toolset and of the config are each tool of the agent: the harness
// adds no tool of its own. An assembly reads them in order. Give tools
// with [Tools]. Two tools with one name make the assembly fail.
//
// The assembly binds each [Binder], such as [SkillTool], [AgentTool],
// and a tool of [Sandboxed], and skips each tool of [tool.Named].
//
// A deferred tool, from [tool.Deferred] or [tool.Defer], goes to the
// agent with the others. See [agent.Toolset].
func Toolset(readers ...ToolsReader) Option {
	return func(h *Harness) { h.tools = append(h.tools, readers...) }
}

// Sandbox sets the sandbox of the tools of [Sandboxed]. The Dir of box
// is then a root of the permission policy, in place of the Dir of the
// config. Without Sandbox, each assembly makes a [sandbox.Local] in the
// Dir of the config. That sandbox has no fence unless the harness has
// [Fence].
func Sandbox(box sandbox.Sandbox) Option {
	return func(h *Harness) { h.box = box }
}

// Fence puts the default sandbox in the [fence.Workspace] policy. A
// command then writes only in the Dir and the AddDirs of the config,
// and the temp folder. It can read each path. opts go after the
// Workspace policy, so they can change it. Each Fence adds its opts.
// Without Fence, the default sandbox has no fence, and the permission
// policy is the only check. The limit Fence of the config also turns
// it on.
//
// The fence runs only on macOS. On another OS, an assembly with a tool
// of [Sandboxed] fails until a caller gives a sandbox with [Sandbox]. A
// sandbox of Sandbox ignores Fence.
func Fence(opts ...fence.Option) Option {
	return func(h *Harness) {
		h.fenced = true
		h.fence = append(h.fence, opts...)
	}
}

// System sets the base text of the system prompt. The harness sends it
// as the first section, named [SystemSection], and the prompt builder
// reads it as [Assembly.System].
func System(s string) Option {
	return func(h *Harness) { h.system = s }
}

// Prompt sets the builder of the system prompt. Without it, or with
// nil, the harness uses [DefaultPrompt].
func Prompt(b PromptBuilder) Option {
	return func(h *Harness) { h.prompt = b }
}

// Seed adds builders of the first entries of a new session. The harness
// runs them in order. Without a seeder, the harness uses [DefaultSeed].
// To keep the environment of DefaultSeed next to other seeders, give it
// too. To seed nothing, use [NoSeed].
func Seed(seeders ...Seeder) Option {
	return func(h *Harness) { h.seeds = append(h.seeds, seeders...) }
}

// Allow adds rules for the tool calls that run without a question, such
// as "bash(go test *)". See [permission.Policy].
func Allow(rules ...string) Option {
	return func(h *Harness) { h.allow = append(h.allow, rules...) }
}

// AgentDefaults adds options for the agent, such as [agent.Thinking].
// The options of the config go after these, and the harness sets
// [agent.System] and [agent.Store] last, so the prompt of the harness
// and the store of the config win. An [agent.Toolset] here adds its
// tools after the tools of [Toolset], and [Assembly.Tools] does not
// list them. Give the tools with Toolset.
//
// The harness uses the strategy Summarize of the package compact by
// default. To use another strategy, give [agent.Compaction] here or the
// Compaction of the config. To turn compaction off, give
// agent.Compaction(nil).
func AgentDefaults(opts ...agent.Option) Option {
	return func(h *Harness) { h.agentOpts = append(h.agentOpts, opts...) }
}
