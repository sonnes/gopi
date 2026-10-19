---
title: Skill
description: How a skill reaches the model on demand through the skill tool, how the system prompt lists the skills, and how two skills with one name both stay.
package: pkg/harness, pkg/harness/skill, pkg/harness/files
read_when:
  - You give an agent skills from SKILL.md files or from code
  - A skill does not reach the model, or the wrong skill does
  - The model cannot read a support file of a skill
---

# Skill

A skill is a named body of instructions that the model loads on demand. The `skills` section of the system prompt lists the name and the description of each skill. The model calls the skill tool with a name, and the body arrives as the tool result. A skill comes from a `SKILL.md` file or from code.

```
  skills section  - deploy: Deploy the service.      name + description
                  - ~:deploy: Deploy from home.      a hidden skill
       |
  model  -------> skill {"name":"deploy"}
       |
  tool result <-- the body, then the paths of the support files
       |
  model  -------> skill {"name":"deploy","file":"scripts/run.sh"}
```

## Design

- The body reaches the model as a tool result, not in the system prompt. A library of many skills costs only one line for each skill until the model loads one.
- The list of skills is its own section, after the `system` and `instructions` sections. A change to a skill between two runs is then a section change after the history, so the start of the request stays cached.
- The name of a skill is the name of its folder, as in [Claude Code](https://code.claude.com/docs/en/skills). The frontmatter cannot then give two folders one name.
- Each skill has a qualified name: its qualifier, ":", and its name. The qualifier is the folder of the layout relative to the root of the project, or "~" for the home folder, as Claude Code names `apps/web:deploy`. Skills merge by qualified name, so a skill of `.agents` replaces a skill of `.claude` in one folder.
- One skill gets the bare name: the last one, in the order home, the subfolders of `Dir`, the root down to `Dir`, then code. The others keep their qualified names in the list and in the schema. A project then overrides a home skill, and no skill is lost on a clash.
- The skill tool takes the bare name or the qualified name. Its schema lists the names that the model sees, so a provider that checks the schema rejects a name that does not exist.
- The harness adds the skill tool to each assembly that has a skill that the model can call, after the tools of the toolset. A tool with nothing to load only makes the model call it for no result.
- A skill with `disable-model-invocation` stays in the assembly but not in the list or the tool. A caller such as a slash command can still run it.
- The support files of a skill are an `fs.FS`, and the skill tool reads them. A skill from an `embed.FS` then works as a skill on disk does, and the model needs no sandbox path to reach the files.
- The reader parses the frontmatter of Claude Code, and keeps each key that the harness does not use in `Meta`. A file in the format of Claude Code or Codex then loads, and a caller can read a key that gopi does not know.
- If the YAML fails, the reader quotes each plain value and parses it again, as Claude Code and Codex do. A file that still fails, or that has no description, is skipped, so one typo does not stop every session.

## Map

| Task                                        | File                               |
| ------------------------------------------- | ---------------------------------- |
| Change what a SKILL.md file must hold       | `pkg/harness/files/frontmatter.go` |
| Change how skills are read from a folder    | `pkg/harness/files/skills.go`      |
| Change how skills merge or get their names  | `pkg/harness/assembly.go`          |
| Change what the skill tool takes or returns | `pkg/harness/skill_tool.go`        |
| Change how the prompt lists the skills      | `pkg/harness/prompt.go`            |

## Related Concepts

- [Harness](harness.md)
- [Tool](../tool/tool.md)
- [Section](../agent/section.md)
