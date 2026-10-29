---
title: Child Session
description: How a host keeps the session of a subagent as a read-only session that follows the run of its parent, also in the background and after the run.
package: pkg/host
read_when:
  - You show the subagents of a hosted session
  - A child session has a wrong session status
  - A child in the background stops, or does not stop
---

# Child Session

A child session is the session of one subagent run, with its parent in a session value. In a [host](host.md), it is a session with a `Parent`. The run of the parent writes it, and a reader reads it as it reads any session.

## Design

- The run of the parent writes the child session, so `Send`, `Update`, and `Interrupt` on a child fail with `ErrChild`.
- The session status of a child follows the [subagent events](../agent/subagent.md) of the parent, and its stream has the events of the child without their wrapper. A reader then shows a child as it shows any session.
- A child session has the `Title` of its `subagent_start`, or its name if the start has no title. The host keeps the title only in memory, so a child that the host recovers from the store has no title.
- A child in the background runs after the run of its parent: its `subagent_end` with the status `background` keeps it running. `RunsChildren` tells a reader that the work of a session goes on.
- The host gives each agent an `Idle` function, so the events of the child between runs reach the child and the stream of the parent. A stop of the session ends each child, because no child outlives its agent.
- If the agent of the parent is a waker, it starts a turn on its own, as Claude Code does after a child in the background ends. With no run, the host runs that turn at once, and during a run, after the run.

## Map

| Task                                                    | File                  |
| ------------------------------------------------------- | --------------------- |
| Change how the session of a subagent follows its parent | `pkg/host/child.go`   |
| Change how a wake of the agent starts a run             | `pkg/host/session.go` |
| Change the session status of a child                    | `pkg/host/status.go`  |

## Related Concepts

- [Host](host.md)
- [Subagent](../agent/subagent.md)
- [Agent Tool](../harness/agent-tool.md)
