---
title: Definition
description: How an agent, a skill, and an instruction text reach an agent from any store through one source interface.
package: pkg/def
read_when:
  - You give an agent definitions from a folder, a database, or code
  - You read a frontmatter key that gopi does not know
  - You add a store of definitions
---

# Definition

A definition is a part of an agent that a person writes: an agent, a skill, or an instruction text such as `AGENTS.md`. Package `def` holds the types and one interface, `Source`, that gives them as a `Set`. A store gives a source of a folder. `Layers` merges sources, and the harness, each driver of a CLI, and the host load the result.

```
  storefs.Dir, storefs.FS, def.Set, ...
              |
          def.Layers ---Load---> harness: instructions, skills, agents
                                 CLI driver: files for its CLI
                                 host: the agent file of a session
```

## Design

- `def` reads no file and imports only the standard library and `pkg/ai`. A store then needs only this package to give definitions, and the harness, the host, and each store share one type.
- A `Set` is a source of itself. A definition in code then goes in the list of `Layers`, at the place where it must win or lose. Its `Load` gives new lists, so a driver can change its copy.
- `Layers` holds the one merge rule: a later definition wins at the position of the earlier one, and the instructions follow in order. Each driver loads the same layers, so each driver shows the same names and the same winner for a clash.
- The fields keep the values as the file writes them, such as a model spec or a permission mode. The harness maps them, so a store needs no catalog.
- A store keeps each key that no field takes in `Extra`, with the value as the store parsed it. A file in the format of Claude Code, Codex, or Agent Skills then loads. A caller reads a key as its own type with `Extra.Read`, because only the caller knows the shape of its own keys.
- `Agent.InheritsModel` holds the rule of the "inherit" model, with no case, so the harness, the host, and the CLI read an agent file the same way.
- `storefs.Dir` opens a folder on disk as an `os.Root`. `os.DirFS` follows a symbolic link out of the folder, and the skill tool reads the support files of a skill for the model. The root closes when its FS is unreachable, because the skill tool reads files after the assembly.
- A writer of definition files fails on a name that is not one file name, because a name comes from a folder that a person can clone. A skipped definition would go away with no message.

## Map

| Task                                       | File                    |
| ------------------------------------------ | ----------------------- |
| Change the fields of an agent              | `pkg/def/agent.go`      |
| Change the fields of a skill               | `pkg/def/skill.go`      |
| Change the merge or a lookup by name       | `pkg/def/source.go`     |
| Change how a caller reads an unknown key   | `pkg/def/extra.go`      |
| Change how a folder or an `fs.FS` is read  | `pkg/store/fs/dir.go`   |
| Change how the files of a CLI are written  | `pkg/store/fs/write.go` |

## Related Concepts

- [Skill](../harness/skill.md)
- [Agent Tool](../harness/agent-tool.md)
- [Harness](../harness/harness.md)
