---
title: CLI Request
description: How a CLI agent answers the requests of its CLI, an approval or a hook call, with the approve function and the hooks of gopi.
package: pkg/agent/claude, pkg/agent/codex
read_when:
  - You answer the permission prompts of Claude Code from Go
  - You answer the approval requests of Codex from Go
  - A gopi hook does not run on a CLI agent
  - A turn of a CLI stops and waits
---

# CLI Request

A CLI asks its [CLI agent](cli-agent.md) during a turn: an approval of a tool call, or a call of a hook. The agent answers an approval with the approve function, and a hook call with the gopi hooks. Claude Code sends each request over its control protocol. Codex sends only approvals over its protocol, and calls no gopi hook.

## Design

- The agent answers every request of the CLI, and an unknown request gets an error. The CLI waits for an answer, so a request with no answer stops the turn.
- Both agents take the approve function of the harness, `agent.ApproveFunc`, so one function answers the approvals of each agent. The function gets the tool call as the log has it, so a rule for the log matches the approval too. Without an `Approve` function, the agent declines each approval.
- Codex cannot change the input of an action or send a reason to the model, so the Codex agent declines a changed input and drops the reason.
- Neither agent keeps a rule for an `Always` decision, and neither CLI gets one. A CLI matches a rule in its own way, so the caller keeps the grant with the rules of gopi.
- Neither CLI sends the type of a safety check. Each agent marks a safety check with the protected paths of gopi, so a rule or a grant cannot allow it.
- A gopi hook that returns `agent.Ask` asks a person. The Claude Code agent asks its approve function inside the hook, as the loop does, so an ask gets the same answer on both drivers. Claude Code runs the call when a hook times out, so the agent gives the PreToolUse hook a time limit that is longer than a person takes to answer.
- The Codex approve function runs in its own goroutine, so the reader goes on while a caller decides.
- Claude Code calls the gopi hooks over the control protocol. The agent registers one callback for each hook event in `initialize`, so the CLI runs no command and needs no trust for a hook.
- Codex has no hook callback over its protocol, and a hook of Codex is a command. So the Codex agent calls no gopi hook, ignores the hooks of its config, and needs no program other than `codex`. The cost: on Codex, only an approval request reaches the caller before a call runs.
- A hook of a CLI agent gives text entries only, because a CLI takes only text context from a hook. Claude Code has no hook at a model call, so its driver rejects `BeforeCall` and `AfterCall`.
- The Claude Code agent maps each hook point to the hook events of the CLI in one map.

## Map

| Task                                       | File                          |
| ------------------------------------------ | ----------------------------- |
| Change a control request of Claude Code    | `pkg/agent/claude/process.go` |
| Change how Claude Code calls a hook        | `pkg/agent/claude/hooks.go`   |
| Change the Codex handshake or a request    | `pkg/agent/codex/conn.go`     |
| Change the hook wire of Claude Code        | `pkg/agent/internal/clihook/clihook.go` |

## Related Concepts

- [CLI Agent](cli-agent.md)
- [Driver](driver.md)
- [Loop](loop.md)
- [Permission](../permission/permission.md)
