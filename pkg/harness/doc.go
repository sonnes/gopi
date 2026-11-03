// Package harness assembles a coding agent, an [agent.Agent], from
// options. A [Harness] holds the builders: the catalog and the model
// spec, the readers of [Defs], [Instructions], [Skillset], [Agents],
// and [Toolset], the permission rules, a [PromptBuilder], and the
// seeders. [Harness.New] and [Harness.Durable] assemble an agent from
// them on each call.
//
// The harness reads no file on its own, and adds no tool on its own.
// The caller gives each folder of definitions as a reader, such as a
// folder of package github.com/sonnes/gopi/pkg/store/fs, and each tool
// with [Toolset]. A [Binder] binds to each assembly: [SkillTool] loads a
// skill for the model, [AgentTool] runs a subagent in a child session,
// and a tool of [Sandboxed] runs in the sandbox of the assembly. The
// permission mode and the rules of package permission decide which tool
// calls run. A user message that starts with "/" and the name of a
// skill, such as "/deploy now", loads the skill with no tool, as
// [def.Set.Slash] reads it.
//
// A caller must obey these rules:
//
//   - Give the options that all sessions share to [New], and the options
//     of one session, such as its [Dir], to the call. A call applies the
//     options of New first, then its own.
//   - Give the readers lowest first. A later skill or subagent with the
//     same name replaces an earlier one. A skill with a qualifier keeps
//     its qualified name, such as "ops:deploy", and the last skill with
//     a name takes the bare name. A definition in code is a reader too.
//   - Give every seeder with [Seed]. The seeders run in order, once, in
//     a session with no entries. With no seeder, the harness uses
//     [DefaultSeed].
//   - Give the store with [Store], and the tools with [Toolset]. The
//     harness sets [agent.System] and [agent.Store] after
//     [AgentDefaults], so its prompt and its store win.
//   - Give [Approve] if a tool call can need an approval. Without it, a
//     call that needs an approval is denied.
//
// The reasons are in docs/concepts/harness/harness.md,
// docs/concepts/harness/skill.md, docs/concepts/harness/agent-tool.md, and
// docs/concepts/harness/permission.md.
package harness
