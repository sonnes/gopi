---
title: Harness
description: How a harness assembles a coding agent from options, the definitions of its readers, and a catalog, on each call.
package: pkg/harness, pkg/store/fs
read_when:
  - You build a coding agent with instructions, skills, subagents, and permissions
  - You build one agent for each session of many repositories
  - An agent reads an instruction text or a skill that you do not expect
  - A resumed session runs on the wrong model
---

# Harness

A harness holds the builders of a coding agent: a catalog and a model spec, the readers of the instruction texts, the skills, and the subagents, the built-in tools, the permission rules, a prompt builder, and the seeders. Each call of `New` or `Durable` reads every reader again and resolves one assembly.

```
  New(baseline options)            the builders, shared by every session
     |
     | Durable(ctx, id, call options)
     v
  spec        call Model > session value > baseline Model
     |
  readers     Defs, Instructions, Skillset, Agents, in option order
     |
  skills      merge by qualified name, the last one wins
  subagents   merge by name, the last one wins
  tools       built-in, Toolset, skill tool, task tool
     v
  Assembly ---> System + prompt builder --> agent.System
     |      ---> permission hook ---------> agent.Hooks, first
     v
  agent.Durable --> seeders in order, only if the log is empty
```

## Design

- The harness is one set of options, as `pkg/agent` is. A call applies the options of `New`, then its own, to a copy, so one harness serves many sessions at the same time. The cost: no single value holds the setup to print or to load from a file.
- The harness reads no file on its own. The caller gives each folder of definitions as a reader, lowest first, so gopi has no opinion about folder layouts, and the caller picks what each session reads. The cost: a project set up for Claude Code loads only when the caller names its folders.
- The harness assembles on each call and caches nothing. An edit to an instruction file or a skill then reaches the next agent. A reader that is slow caches on its own, because only it knows when its data changes.
- The prompt builder returns sections, and the agent calls it before each model call. A resumed session then gets the definitions that the readers give now, and an edit is a section change after the history, so the start of the request stays cached.
- `DefaultPrompt` writes no fact that changes between sessions. The date and the working directory go in the seed, which a new session gets once.
- A durable session keeps its model spec in a session value. A resume then runs on the same model, and a `Model` on the call changes it. The harness reads the value before it opens the session, so the store is a harness option and not an agent option.
- The harness sets the permission hook first and `agent.System` and `agent.Store` after the agent options of the caller. A hook of the caller then sees only the calls that the rules let run, and the prompt and the store of the harness win.
- The default sandbox has the workspace fence. A coding agent then cannot write outside its folder by default. The fence runs only on macOS, so on another OS the assembly fails with a message that names `Sandbox`.

## Map

| Task                                              | File                         |
| ------------------------------------------------- | ---------------------------- |
| Change the steps of one call                      | `pkg/harness/harness.go`     |
| Change how skills and tools are merged            | `pkg/harness/assembly.go`    |
| Change the built-in tools or the default sandbox  | `pkg/harness/builtin.go`     |
| Change the default prompt or the default seed     | `pkg/harness/prompt.go`      |
| Change how a SKILL.md or an agent file is parsed  | `pkg/store/fs/`              |
| Change the flags or the folders that `gopi` gives to the harness | `cmd/gopi/main.go` |

## Related Concepts

- [Definition](../def/definition.md)
- [Skill](skill.md)
- [Subagent](subagent.md)
- [Permission](permission.md)
- [Catalog](../catalog/catalog.md)
- [Section](../agent/section.md)
- [Value](../session/value.md)
