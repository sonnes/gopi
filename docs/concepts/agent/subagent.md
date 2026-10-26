---
title: Subagent
description: How a subagent of any driver becomes a child session, and how the run stream of the parent shows its start, its events, and its end.
package: pkg/agent
read_when:
  - You show the work of a subagent in a UI
  - You find the children of a session
  - A driver runs a subagent and you change what it sends
---

# Subagent

A subagent is an agent that one tool call of a parent starts: the agent tool of the harness, the Agent tool of Claude Code, or spawn_agent of Codex. Each subagent has a child session, and the run stream of the parent carries its start, its events, and its end.

```
tool_execution_start  c1
subagent_start        c1  s1.reviewer-a1b2   name, title, prompt, model
subagent              c1  s1.reviewer-a1b2   agent_start
subagent              c1  s1.reviewer-a1b2   ... each event of the child
subagent              c1  s1.reviewer-a1b2   agent_end
subagent_end          c1  s1.reviewer-a1b2   status, last text
tool_execution_end    c1
```

## Design

- A child keeps its messages in its own session, and its parent in the `Parent` value: the parent session and the tool call. The link is data, so a store needs no folder layout to keep it.
- The ID of a child is the parent ID, ".", the name, "-", and a suffix. A driver takes the suffix from the native ID of the subagent if it has one, so that a replay of the same output finds the same child.
- The parent stream wraps each event of a child in a `subagent` event. A viewer of the parent then sees the child live, with no second stream. The order rules of a run apply inside each child, and not across the parent and the child.
- A tool sends the events of its child through `EmitSubagent`, which takes only the three subagent events and fills the ID of the call. A tool then cannot break the order of the run.
- The host makes each child a read-only session of its own, as [Host](../host/host.md) says.
- Each `subagent_start` has one `subagent_end` in the same run. A run that stops ends each open child as `interrupted`, so a viewer never keeps a child open.
- Claude Code marks each line of a subagent with the ID of its Agent call. Its `task_started` line comes before that call starts on the stream, so the child waits for the start of its call. The CLI does not send the last message of a subagent as a line, so the summary of its `task_notification` becomes the last message of the child.
- Claude Code runs with no background tasks. A background task ends after the result of the run, when no run reads its lines.
- A Codex subagent is a thread of its own in the same server, and the driver routes each notification by its thread ID. The spawn is a `spawn_agent` call, and each other collab call has the name that the model sees. The server does not send the prompt of a spawn, so a Codex child has no prompt.
- Codex does not start a turn when a child ends after the turn of the parent. So the run waits for each child, and gives the model the result of a late child in one more turn.

## Map

| Task                                       | File                       |
| ------------------------------------------ | -------------------------- |
| Read the subagent events and the value     | `pkg/agent/subagent.go`    |
| Change how a tool call gets its sink       | `pkg/agent/toolcall.go`    |
| Change the order rules that the tests check | `pkg/agent/internal/emittest/check.go` |
| Change how Claude Code lines reach a child | `pkg/agent/claude/subagent.go` |
| Change how Codex threads reach a child     | `pkg/agent/codex/subagent.go` |

## Related Concepts

- [Run Stream](run-stream.md)
- [CLI Agent](cli-agent.md)
- [Agent Tool](../harness/agent-tool.md)
- [Value](../session/value.md)
