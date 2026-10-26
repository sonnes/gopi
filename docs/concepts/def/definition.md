---
title: Definition
description: How an agent, a skill, and an instruction text reach an agent from any store through one reader interface for each.
package: pkg/def
read_when:
  - You give an agent definitions from a folder, a database, or code
  - You read a frontmatter key that gopi does not know
  - You add a store of definitions
---

# Definition

A definition is a part of an agent that a person writes: an agent, a skill, or an instruction text such as `AGENTS.md`. Package `def` holds the types and a reader interface for each. A store implements the readers, and the harness takes them.

```
  store (fs, code, ...) --ReadAgents-------> harness: "agents" section, agent tool
                        --ReadSkills-------> harness: "skills" section, skill tool
                        --ReadInstructions-> harness: "instructions" section
```

## Design

- `def` reads no file and imports only the standard library and `pkg/ai`. A store then needs only this package to give definitions, and the harness, the host, and each store share one type.
- An `Agent` and a `Skill` are readers of themselves. A definition in code then goes in the list of readers, at the place where it must win or lose.
- The fields keep the values as the file writes them, such as a model spec or a permission mode. The harness maps them, so a store needs no catalog.
- A reader keeps each key that no field takes in `Extra`, with the value as the reader parsed it. A file in the format of Claude Code, Codex, or Agent Skills then loads. A caller reads a key as its own type with `Extra.Read`, because only the caller knows the shape of its own keys.
- A folder on disk goes to the readers as the FS of an `os.Root`. `os.DirFS` follows a symbolic link out of the folder, and the skill tool reads the support files of a skill for the model. The root closes when its FS is unreachable, because the skill tool reads files after the assembly.
- `FindAgent` returns the last agent with a name, as the merge of the harness does. The host then picks the same agent from a list of folders as the harness does.

## Map

| Task                                       | File                 |
| ------------------------------------------ | -------------------- |
| Change the fields of an agent              | `pkg/def/agent.go`   |
| Change the fields of a skill               | `pkg/def/skill.go`   |
| Change a reader interface                  | `pkg/def/reader.go`  |
| Change how a caller reads an unknown key   | `pkg/def/extra.go`   |
| Change how the host finds an agent by name | `pkg/def/find.go`    |
| Change how a folder on disk is read        | `pkg/store/fs/dir.go` |

## Related Concepts

- [Skill](../harness/skill.md)
- [Agent Tool](../harness/agent-tool.md)
- [Harness](../harness/harness.md)
