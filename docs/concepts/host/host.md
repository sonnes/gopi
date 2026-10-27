---
title: Host
description: How a host keeps the sessions of any gopi agent for a Go program, as resources with an ID, a config, and a status that survive a restart.
package: pkg/host
read_when:
  - You run sessions of agents in a Go program
  - A session shows a wrong status after a restart
  - A tool call waits and no one answers it
---

# Host

A host keeps the sessions of gopi agents for a Go program. A session is a resource with an ID. The program creates it with a `Config`, and then sends it user events by its ID: a message, an interrupt, or a confirmation. The host starts the agent of a session through the [driver](../agent/driver.md) of its model spec, and reports each change of its status.

```
  Create(config) ---> store: record, gopi.host/config, gopi.host/session
                      |
  Send(id, m) ---> agent? no: Load, then Driver.Start(config)
                      |
                      v
  hosted session ------- one run at a time -------> Runner
    |      ^                                          |
    |      | Confirm(id, call)                        | agent events
    |      +--- approve waits ---+                    v
    v                                           agent hub
  gopi.host/session ---> host hub                     |
                           |                          v
                           v                    Host.Stream
                      Host.Events
```

## Design

- A caller names a session by its ID, and holds no handle. The host knows each session of the store, also one with no agent, so a read never asks if an agent runs.
- The user events follow the user events of [Claude Managed Agents](https://platform.claude.com/docs/en/managed-agents/overview): `Send` is `user.message`, `Interrupt` is `user.interrupt`, and `Confirm` is `user.tool_confirmation`. The status names follow it too.
- `Create` keeps the config in the value `gopi.host/config`, with the fields of its agent file, before the driver starts. Each start of the agent reads that value, so a session keeps its agent, its model, and its folder after a restart. The cost: an edit of the model, the mode, the effort, the tools, or the skills of an agent file reaches only a new session.
- The config holds no prompt. Before each run, the host reads the prompt of the agent file and calls the function of `Sections` for the session, and the driver sends only the [sections](../agent/section.md) that changed. A prompt depends on a state that changes during a session, so a stored copy goes stale.
- `Update` writes the new config, and the next run starts the agent again from it. The active run keeps its agent, because a driver reads the config only when it starts the agent. A change of the ID, the folders, the agent file, or the kind of agent fails, because the new agent cannot resume the conversation.
- The host writes the session to `gopi.host/session` at each change. `Open` makes each session that ran or waited idle, with the stop reason `restarted`, because no agent survives a restart.
- A failed run is idle with the stop reason `error`. A session then has three statuses, and the stop reason tells the end of the last run.
- A message to a running session steers it, if the agent is an `agent.Steerer`. Otherwise the host holds the message, and it runs after the run. A message is then never lost.
- The host gives each agent its own approve function. A call waits for `Confirm` on a channel of its own, so two or more calls wait at the same time and end in any order. With no person, a call waits until its run ends. So a program with no person confirms from the host stream, or picks a mode that asks for no call.
- Each event of the host stream carries the whole session. A reader then needs no history, and `State.Apply` only sets the session.
- Each `Events` and `Stream` call gives a copy of the state and each event after it, under one lock, so a reader gets no gap and no repeat. A reader that falls too far behind loses its stream, so one stalled reader cannot hold the events in memory.
- The session of a subagent is a session of the host with a `Parent`. The run of the parent writes it, so `Send`, `Update`, and `Interrupt` fail with `ErrChild`. Its status follows the subagent events of the parent, and its stream has the events of the child without their wrapper. A reader then shows a child as it shows any session.
- The session of a subagent has the `Title` of its `subagent_start`, or its name if the start has no title. The host keeps the title only in memory, so a child that the host recovers from the store has no title.
- A child in the background runs after the run of its parent: its `subagent_end` with the status `background` keeps it running. The host gives each agent an `Idle` function, so the events of the child between runs reach the child and the stream of the parent. `RunsChildren` tells a reader that the work of a session goes on. A stop of the session ends each child, because no child outlives its agent.
- An agent that is an `agent.Waker` can start a turn on its own, as Claude Code does after a background child ends. The agent then calls the `Wake` function that the host gives it. With no run, the host runs that turn with `Continue` at once. During a run, the turn runs after the run.
- One host owns a store. Two hosts on one store must not run the same session, because their entries interleave in one log.

## Map

| Task                                                | File                    |
| --------------------------------------------------- | ----------------------- |
| Change how a session is created, read, or listed    | `pkg/host/host.go`      |
| Change the status values, or the recovery at Open   | `pkg/host/status.go`    |
| Change how an agent file fills a config             | `pkg/host/agentfile.go` |
| Change the sections that a session gets             | `pkg/host/agentfile.go` |
| Change how the agent of a session starts or stops   | `pkg/host/session.go`   |
| Change how the status follows the run and the calls | `pkg/host/session.go`   |
| Change how a message steers, waits, or runs         | `pkg/host/run.go`       |
| Change how a wake of the agent starts a run         | `pkg/host/session.go`   |
| Change how a call waits for Confirm                 | `pkg/host/approval.go`  |
| Change what Update can change                       | `pkg/host/update.go`    |
| Change the host events or the state of a reader     | `pkg/host/event.go`     |
| Change how a reader gets the state and the events   | `pkg/host/hub.go`       |
| Change how the session of a subagent follows its parent | `pkg/host/child.go` |

## Related Concepts

- [Driver](../agent/driver.md)
- [Section](../agent/section.md)
- [Definition](../def/definition.md)
- [Catalog](../catalog/catalog.md)
- [Run Stream](../agent/run-stream.md)
- [Log](../session/log.md)
