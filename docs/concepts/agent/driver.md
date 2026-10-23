---
title: Driver
description: How one interface starts the harness, Claude Code, or Codex for a session, and what a driver does with a field or a hook point that its agent cannot honor.
package: pkg/agent, pkg/harness, pkg/agent/claude, pkg/agent/codex
read_when:
  - You start an agent of any kind for a session
  - You add a driver for another agent
  - A driver rejects a config field or a hook
  - You show whether an agent CLI is installed and signed in
---

# Driver

A driver starts an agent of one kind for one session. The harness, the Claude Code agent, and the Codex agent each have one. The caller gives a driver one config: the session and its store, the model, the prompt text, the skills, the permission mode, the rules, the approve function, the hooks, and the tools. The driver returns a runner, the part of the agent that the caller uses.

```
  Driver.Start(ctx, Config)
    |-- harness ........ Harness.Durable
    |-- claude-code .... claude.Durable
    +-- codex .......... codex.Durable
    |
  Runner: Run, Log, State
    + Steerer ......... the harness only
    + io.Closer ....... the CLI agents only
```

## Design

- One interface starts each kind of agent, so a caller has one code path for the harness and the CLIs. Each agent keeps its own type and its own options, and its driver maps the config to them.
- A runner shows an extra feature with another interface, such as `Steerer` or `io.Closer`. The caller asks with a type assertion and fills the gap when the answer is no. The cost: a new feature needs a new interface.
- If a driver cannot honor a field of the config, `Start` fails with an error that wraps `errors.ErrUnsupported` and names the field. An agent that drops a rule or a tool does something that its caller did not ask for. The doc comment of each `Driver` function names the fields that it rejects, and the `Hooks` option of each CLI agent names its hook points.
- A driver of a CLI skips `Dirs`, and does not fail. A CLI loads its own skills and agents from its own folders, and the caller manages them, so the dirs of the config are for the harness.
- The config names a permission mode with the names of Claude Code, because the harness uses the same names. A driver that cannot honor the mode fails `Start`, as for a field. Codex has a sandbox and a policy for commands, and no rule for each tool, so it honors only plan and bypassPermissions.
- Each hook point is its own interface, so a driver sees the points that a hook uses. If the agent cannot call a point, `Start` fails and names the point, because a hook that guards a call must not miss one. The cost: one set of hooks does not run on every driver.
- A hook that implements no point fails `Start`, because it is a hook with a misspelled method.
- The approve function and the hook points have the same types for each driver, so a caller writes them once. The tool names in a call are the names of the agent: Claude Code sends `Bash`, and the harness and Codex send `bash`.
- A driver of a CLI reads the model ID from the resolved model. The harness reads the spec, because it resolves the spec in its own catalog.
- A driver of a CLI lists the models of the installed CLI with `ListModels`, over the protocol that a run uses: the initialize response of Claude Code, and `model/list` of Codex. The CLI starts as for a run, so the hooks of its settings run. Claude Code lists each alias, such as `opus`, and gopi keeps the aliases next to the full IDs, because `--model` takes both.
- A list fails after 30 seconds with no answer, because a CLI that waits for a login does not exit. A failed list of Codex kills the app server at once.
- A driver can be a `Checker`, which reports its status: the version of what it runs, and the account that is signed in. A check uses the setup of the driver, so a caller checks what a session would start. The CLI drivers run the version and the sign-in commands of the CLI with the binary and the environment of the driver. The status also gives the account: how the driver signs in, the plan or the cloud, the email, and the organization. It never holds a key or a token. It can hold an email, so the caller decides who sees it.
- Claude Code gives the account in `claude auth status --json`. `codex login status` gives only the sign-in method, so the Codex driver also reads the claims of the ID token in `auth.json` of the Codex home. That file is a private format of Codex. If the driver cannot read it, the status keeps the method and the name from the status line.
- The thinking levels of a listed model replace the registered levels, because the CLI knows the levels that it takes. A new version of a CLI can add a level.

## Map

| Task                                    | File                         |
| --------------------------------------- | ---------------------------- |
| Change the config or the runner         | `pkg/agent/driver.go`        |
| Change how the harness takes the config | `pkg/harness/driver.go`      |
| Change how Claude Code takes the config | `pkg/agent/claude/driver.go` |
| Change how Codex takes the config       | `pkg/agent/codex/driver.go`  |
| Find the hook points of a CLI agent     | `pkg/agent/<cli>/hooks.go`   |
| Change how a CLI lists its models       | `pkg/agent/<cli>/models.go`  |
| Change how a CLI reports its sign-in    | `pkg/agent/<cli>/check.go`   |

## Related Concepts

- [CLI Agent](cli-agent.md)
- [Loop](loop.md)
- [Harness](../harness/harness.md)
- [Permission](../harness/permission.md)
