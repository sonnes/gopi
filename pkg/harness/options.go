package harness

import (
	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/catalog"
	"github.com/sonnes/gopi/pkg/def"
	"github.com/sonnes/gopi/pkg/harness/permission"
	"github.com/sonnes/gopi/pkg/sandbox"
	"github.com/sonnes/gopi/pkg/session"
)

// Option configures a [Harness], or one call of [Harness.New] or
// [Harness.Durable]. A call applies its options to a copy of the
// harness, on top of the options of New. So an option that sets one
// value, such as [Model], replaces the value of New. An option that
// adds to a list, such as [Skillset], adds after the list of New.
type Option func(*Harness)

// Catalog sets the catalog that resolves the model spec. An assembly
// with no catalog fails.
func Catalog(c *catalog.Catalog) Option {
	return func(h *Harness) { h.cat = c }
}

// Model sets the model spec, such as "anthropic/claude-sonnet-5". On
// [New], it is the default. [Harness.Durable] keeps the spec of each
// session, so a resumed session keeps its model. A Model on the call
// changes the model of the session.
func Model(spec string) Option {
	return func(h *Harness) { h.spec = spec }
}

// Store sets the store of [Harness.Durable]. Without it, the sessions
// of a harness live in one memory store and end with the process. Store
// is a harness option and not an [agent.Option], because the harness
// reads the model of a session from the store before it makes the
// agent.
func Store(s session.Storer) Option {
	return func(h *Harness) { h.store = s }
}

// Dir sets the folder that the agent works in. The built-in tools start
// in it. Without Dir, the harness uses the working directory of the
// process. The harness reads no file of Dir: give the definitions with
// [Defs].
func Dir(path string) Option {
	return func(h *Harness) { h.dir = path }
}

// Defs adds readers of instructions, skills, and subagents, such as a
// folder of package github.com/sonnes/gopi/pkg/store/fs. It adds each reader to the lists of
// [Instructions], [Skillset], and [Agents]. Without Defs and these
// options, the harness reads no file, so the caller picks every folder
// that an agent reads.
func Defs(readers ...def.Reader) Option {
	return func(h *Harness) {
		for _, r := range readers {
			h.instructions = append(h.instructions, r)
			h.skills = append(h.skills, r)
			h.agents = append(h.agents, r)
		}
	}
}

// Instructions adds readers of instruction texts. The harness reads
// them in order, and [DefaultPrompt] puts their texts in the
// "instructions" section of the system prompt, in the same order. A
// [def.Instruction] is a reader, so a text in code goes in the same
// list.
func Instructions(readers ...def.InstructionsReader) Option {
	return func(h *Harness) { h.instructions = append(h.instructions, readers...) }
}

// Skillset adds readers of skills. The harness reads them in order. A
// later skill with the same qualified name replaces an earlier one, and
// the last skill with a name takes the bare name. A [def.Skill] is a reader, so a skill in code goes
// in the same list.
//
// An assembly with skills gives the model the skill tool, after the
// tools of [Toolset], and [DefaultPrompt] lists the skills in a section
// of the system prompt. An assembly with no skills has no skill tool,
// because a tool with nothing to load only confuses the model.
func Skillset(readers ...def.SkillsReader) Option {
	return func(h *Harness) { h.skills = append(h.skills, readers...) }
}

// Agents adds readers of subagents. The harness reads them in order. A
// later subagent replaces an earlier
// one with the same name. An assembly with subagents gives the model
// the task tool, which runs a subagent in a child session.
func Agents(readers ...def.AgentsReader) Option {
	return func(h *Harness) { h.agents = append(h.agents, readers...) }
}

// Toolset adds readers of the tools that the model can call, after the
// tools of [Builtin]. An assembly reads them in order. Give tools with
// [Tools]. Two tools with one name make the assembly fail. The skill
// tool has the name "skill", and the task tool has the name "task".
//
// A deferred tool, from [tool.Deferred] or [tool.Defer], goes to the
// agent with the others. See [agent.Toolset].
func Toolset(readers ...ToolsReader) Option {
	return func(h *Harness) { h.tools = append(h.tools, readers...) }
}

// MCP adds the MCP servers servers. [Harness.New] and [Harness.Durable]
// connect to them with [mcp.Connect], and the agent gets their tools as
// deferred tools named "mcp__<server>__<tool>". A stdio server gets the
// environment of the process. If a server does not connect, New and
// Durable fail. [agent.Agent.Close] closes the sessions, and a
// subagent shares the sessions of its parent.
func MCP(servers ...agent.MCPServer) Option {
	return func(h *Harness) { h.mcp = append(h.mcp, servers...) }
}

// Builtin sets the built-in tools. Without Builtin, the agent gets
// [Coding].
func Builtin(b Builtins) Option {
	return func(h *Harness) { h.builtin = b }
}

// Sandbox sets the sandbox of the built-in tools. Without Sandbox, the
// harness makes a [sandbox.Local] in [Dir] with the
// [fence.Workspace] policy. The fence runs only on macOS, so on another
// OS an assembly with built-in tools fails until a caller gives a
// sandbox.
func Sandbox(box sandbox.Sandbox) Option {
	return func(h *Harness) { h.box = box }
}

// System sets the base text of the system prompt. The harness sends it
// as the first section, named [SystemSection], and the prompt builder
// reads it as [Assembly.System].
func System(s string) Option {
	return func(h *Harness) { h.system = s }
}

// Preload adds the body of each named skill to the base text of the
// prompt, after the text of [System], as a subagent does with the skills
// of its file. A name is the name of a skill in the skill list, or its
// qualified name. If no skill has a name, the assembly fails.
func Preload(names ...string) Option {
	return func(h *Harness) { h.preload = append(h.preload, names...) }
}

// Sections sets the function that returns more sections of the system
// prompt. The harness puts them after the sections of the prompt
// builder, and the agent calls fn before each model call, so a change
// reaches the next call. A subagent does not get them, because the
// file of the subagent is its prompt. If a name repeats, the run fails.
func Sections(fn agent.SystemFunc) Option {
	return func(h *Harness) { h.sections = fn }
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

// Mode sets the permission mode of each tool call. Without Mode, the
// harness uses [permission.Default].
func Mode(m permission.Mode) Option {
	return func(h *Harness) { h.policy.Mode = m }
}

// Allow adds rules for the tool calls that run without a question, such
// as "bash(go test *)". See [permission.Policy].
func Allow(rules ...string) Option {
	return func(h *Harness) { h.policy.Allow = append(h.policy.Allow, rules...) }
}

// Ask adds rules for the tool calls that need an approval, even if an
// Allow rule or the mode lets them run.
func Ask(rules ...string) Option {
	return func(h *Harness) { h.policy.Ask = append(h.policy.Ask, rules...) }
}

// Deny adds rules for the tool calls that never run, in any mode.
func Deny(rules ...string) Option {
	return func(h *Harness) { h.policy.Deny = append(h.policy.Deny, rules...) }
}

// Approve sets the function that asks a person to approve a tool call.
// Without Approve, each call that needs an approval is denied, and the
// model gets the reason.
func Approve(fn agent.ApproveFunc) Option {
	return func(h *Harness) { h.policy.Approve = fn }
}

// SubagentEvents sets a function that gets each event of each child
// run, with the ID of the task call that started it. The run stream of
// the parent does not carry these events. Without SubagentEvents, the
// harness drops them.
func SubagentEvents(fn func(callID string, e agent.Event)) Option {
	return func(h *Harness) { h.subagentEvents = fn }
}

// AgentDefaults adds options for the agent, such as [agent.Thinking].
// The harness sets [agent.System] and [agent.Store] after these, so the
// prompt of the harness and the store of [Store] win. An
// [agent.Toolset] here adds its tools after the tools of [Toolset], and
// [Assembly.Tools] does not list them. Give the tools with Toolset.
func AgentDefaults(opts ...agent.Option) Option {
	return func(h *Harness) { h.agentOpts = append(h.agentOpts, opts...) }
}
