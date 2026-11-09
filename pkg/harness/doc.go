// Package harness assembles a coding agent, an [agent.Agent], from
// options and an [agent.Config]. A [Harness] holds the builders: the
// catalog, the source of [Defs], the readers of [Toolset], the [Allow]
// rules, a [PromptBuilder], and the seeders. The config holds the
// settings of one session, such as its model spec, its folder, and its
// mode. [Harness.New] and [Harness.Start] assemble
// an agent from both on each call. A harness is an [agent.Driver].
//
// The harness reads no file on its own, and adds no tool on its own.
// The caller gives the definitions as a [def.Source], such as a folder
// of package github.com/sonnes/gopi/pkg/store/fs, and each tool with
// [Toolset]. A [Binder] binds to each assembly: [SkillTool] loads a
// skill for the model, [AgentTool] runs a subagent in a child session,
// and a tool of [Sandboxed] runs in the sandbox of the assembly. The
// permission mode and the rules of package permission decide which tool
// calls run. A user message that starts with "/" and the name of a
// skill, such as "/deploy now", loads the skill with no tool, as
// [def.Set.Slash] reads it.
//
// A caller must obey these rules:
//
//   - Give the options that all sessions share to [New], and the
//     settings of one session, such as its Dir, in the config of the
//     call.
//   - Merge the sources of definitions with [def.Layers], lowest first.
//     A later skill or subagent with the same name replaces an earlier
//     one. A skill with a qualifier keeps its qualified name, such as
//     "ops:deploy", and the last skill with a name takes the bare name.
//     A [def.Set] holds definitions in code.
//   - Give every seeder with [Seed]. The seeders run in order, once, in
//     a session with no entries. With no seeder, the harness uses
//     [DefaultSeed].
//   - Give the tools with [Toolset]. The harness sets [agent.System]
//     and [agent.Store] after [AgentDefaults], so its prompt and the
//     Store of the config win.
//   - Give an Approve function in the config if a tool call can need an
//     approval. Without it, a call that needs an approval is denied.
//
// The reasons are in docs/concepts/harness/harness.md,
// docs/concepts/harness/skill.md, docs/concepts/harness/agent-tool.md, and
// docs/concepts/permission/permission.md.
package harness
