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
- The config names a permission mode with the names of Claude Code, as the harness does. Codex has no rule for each tool, so [`codex.Driver`](https://pkg.go.dev/github.com/sonnes/gopi/pkg/agent/codex#Driver) maps each mode to a sandbox and an approval option of Codex.
- If the agent cannot call a hook point of a hook, `Start` fails, because a hook that guards a call must not miss one. A hook that implements no point fails too, because its method is misspelled. The cost: one set of hooks does not run on every driver.
- The approve function and the hook points have the same types for each driver, so a caller writes them once. The approve function takes one `agent.ApprovalRequest`, so an agent can add a field with no change to each approve function. The tool names in a call are the names of the agent, such as `Bash` for Claude Code and `bash` for the harness.
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
