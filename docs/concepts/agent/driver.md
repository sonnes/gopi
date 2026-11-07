---
title: Driver
description: How one interface starts the harness, Claude Code, or Codex for a session, and what a driver does with a field or a hook point that its agent cannot honor.
package: pkg/agent, pkg/harness, pkg/agent/claude, pkg/agent/codex
read_when:
  - You start an agent of any kind for a session
  - You add a driver for another agent
  - A driver rejects a config field or a hook
  - You show the driver status of an agent CLI
---

# Driver

A driver starts an agent of one kind for one session. The harness, the Claude Code agent, and the Codex agent each have one. The caller gives a driver one config, and the driver returns a runner, the part of the agent that the caller uses.

```
  Driver.Start(ctx, Config)
    |-- harness ........ Harness.Durable
    |-- claude-code .... claude.Durable
    +-- codex .......... codex.Durable
    |
  Runner: Run, Log, State
    + Steerer ......... the harness and claude-code
    + Waker ........... claude-code only
    + io.Closer ....... each runner
  Checker ............. claude-code and codex
```

## Design

- One interface starts each kind of agent, so a caller has one code path for the harness and the CLIs. Each agent keeps its own type and options, and its driver maps the config to them.
- A runner shows an extra feature with another interface, such as a steerer or a waker. The caller asks with a type assertion and fills the gap when the answer is no. The cost: a new feature needs a new interface.
- If a driver cannot honor a field of the config, `Start` fails with an error that wraps `errors.ErrUnsupported` and names the field. An agent that drops a rule or a tool does something that its caller did not ask for. Each `Driver` doc comment names the rejected fields.
- Each driver gets the skills of `Skillset`, then the skills and the agents of `Dirs`, merged with one rule, so each driver shows the same names and the same winner for a clash. A driver of a CLI writes the merged set to a folder for each session at each start of the CLI, because a CLI reads only files. The folder goes away at `Close`, and a set with no definitions removes it. The cost: one more copy of each definition on disk, and a process that dies leaves its folder until the next start.
- A skill name with a slash fails the start of a CLI, because the name is a folder name and a skipped skill would go away with no message. The harness has no folder, so it takes this name.
- `Skillset` holds readers in code, because a program can ship skills with no file on disk. The host keeps them in memory and not in the session file, so a session that started earlier gets them. They are lower than `Dirs`, so a person can replace a skill of the program.
- Claude Code gets the folder with `--add-dir`, because `--plugin-dir` adds the name of the folder to each name, and because an agent file keeps the keys that `--agents` does not take. A subagent gets the model of its file only if the spec has the account of the parent, because a CLI runs on one account. Codex gets the skills with `skills/extraRoots/set`, and each agent as a role in the config of the thread.
- `AddDirs` names the folders outside `Dir` that the agent can write. Claude Code gets one `--add-dir` for each folder. Codex gets them as `sandbox_workspace_write.writable_roots`, which only the `workspace-write` sandbox reads. The harness gives them to its permission policy as roots, and to `fence.Workspace` when the fence is on. `Dirs` does not do this job, because it holds only definitions.
- `Limits` are hard limits that hold in each mode, also in bypass mode. Each driver maps a limit to the control of its agent: the harness to its permission policy and its fence, Claude Code to its settings, and Codex to the `workspace-write` sandbox. Codex has no control for reads outside the roots, so that limit fails its start. The zero `Limits` sets no limit, so a caller asks for each limit by name.
- A Codex role has no key for the tools or the turn limit of an agent, so a role runs with each tool of the session. The driver gives the role and does not fail, because an agent file with a `tools` key is common. Codex 0.163 also runs a role with the sandbox of the session, even if the role file sets `sandbox_mode`, so the file does not hold the mode of an agent. The cost: a read-only agent can write on Codex.
- A driver of a CLI adds no text for `Skills`, because the CLI lists each skill and loads its body. The harness adds the body to the prompt. A name of `Skills` that no dir has fails `Start` on each driver.
- A driver of a CLI writes a slash call in the form that its CLI loads, with the name that the CLI knows. Claude Code loads a skill only from the start of the last text block of a user line, so the driver moves the text of the call after the files. Codex loads a skill from a mention, so the driver writes `$` for `/`. A text that names no skill goes as it is.
- A driver of a CLI does not read the `AGENTS.md` of `Dirs`. The CLI also keeps the skills and the agents of its project folder. Codex also keeps those of its home folder, and Claude Code skips `~/.claude`, because it reads no user settings.
- The config has only Deny rules. A [host](../host/host.md) checks the Allow rules and the Ask rules for each driver, so one rule matches the same calls on each driver. A driver of a CLI gives its CLI only the Deny rules that name a whole tool, because a CLI matches a rule with content in its own way.
- A driver of a CLI keeps the permission settings of the user out of the CLI. Then only the caller decides a call that the CLI asks about. The exec policy rules of Codex still apply, because Codex has no key that turns them off.
- The config names a permission mode with the names of Claude Code, as the harness does. Codex has no rule for each tool, so [`codex.Driver`](https://pkg.go.dev/github.com/sonnes/gopi/pkg/agent/codex#Driver) maps each mode to a sandbox and an approval option of Codex.
- If the agent cannot call a hook point of a hook, `Start` fails, because a hook that guards a call must not miss one. A hook that implements no point fails too, because its method is misspelled. The cost: one set of hooks does not run on every driver.
- The approve function and the hook points have the same types for each driver, so a caller writes them once. The approve function takes one `agent.ApprovalRequest`, so an agent can add a field with no change to each approve function. Each driver gives the gopi name of a tool, as [Tool Name](../tool/name.md) describes, so a rule and a hook also match on each driver.
- A driver of a CLI reads the model ID of the resolved model, and the harness resolves the spec in its own catalog.
- A checker reports its [driver status](https://pkg.go.dev/github.com/sonnes/gopi/pkg/agent#Status) with the setup of the driver, so a caller checks what a session starts with.

## Map

| Task                                    | File                         |
| --------------------------------------- | ---------------------------- |
| Change the config or the runner         | `pkg/agent/driver.go`        |
| Change how the harness takes the config | `pkg/harness/driver.go`      |
| Change how Claude Code takes the config | `pkg/agent/claude/driver.go` |
| Change how Codex takes the config       | `pkg/agent/codex/driver.go`  |
| Find the hook points of a CLI agent     | `pkg/agent/<cli>/hooks.go`   |
| Change how a CLI lists its models       | `pkg/agent/<cli>/models.go`  |
| Change the driver status of a CLI       | `pkg/agent/<cli>/check.go`   |

## Related Concepts

- [CLI Agent](cli-agent.md)
- [Loop](loop.md)
- [Harness](../harness/harness.md)
- [Permission](../harness/permission.md)
