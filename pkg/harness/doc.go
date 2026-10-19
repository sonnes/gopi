// Package harness assembles a coding agent, an [agent.Agent], from
// options. A [Harness] holds the builders: the catalog and the model
// spec, the [Sources] of the instruction files, the skills, and the
// subagents, the readers of [Skillset], [Agents], and [Toolset], the
// built-in tools, the permission rules, a [PromptBuilder], and the
// seeders. [Harness.New] and [Harness.Durable] assemble an agent from
// them on each call.
//
// The sources are the files of Claude Code, Codex, and gopi in the home
// folder and in the project: CLAUDE.md and AGENTS.md, the skill folders
// in .claude, .codex, .agents, and .gopi, and the subagent files in
// .claude/agents and .agents/agents. An assembly with skills gets the
// skill tool, which loads a skill for the model. An assembly with
// subagents gets the task tool, which runs a subagent in a child
// session. The permission mode and the rules of package permission
// decide which tool calls run.
//
// A caller must obey these rules:
//
//   - Give the options that all sessions share to [New], and the options
//     of one session, such as its [Dir], to the call. A call applies the
//     options of New first, then its own.
//   - Give the skill readers lowest first. A later skill with the same
//     bare name takes the bare name, and an earlier one keeps its
//     qualified name, such as "apps/web:deploy" or "~:deploy". A skill in
//     code is a reader too, and it comes after the files.
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
// docs/concepts/harness/skill.md, docs/concepts/harness/subagent.md, and
// docs/concepts/harness/permission.md.
package harness
