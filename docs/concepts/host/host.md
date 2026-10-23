---
title: Host
description: How a host runs sessions of any gopi agent for a Go program, and how a host stream and a stream for each session report what happens.
package: pkg/host
read_when:
  - You run sessions of agents in a Go program
  - A reader of a stream misses an event
---

# Host

A host runs sessions of gopi agents for a Go program. The caller gives a `Spec` to `Start`, and the catalog resolves its model spec to a [driver](../agent/driver.md). The host stream reports the state of each session. Each session has its own stream of agent events.

```
  Spec ---> Start: Catalog.Driver, then Driver.Start
              |
              v
  Session --- worker, one run at a time ---> Runner
    |                                          |
    | session, input, run,                     | agent events
    | and approval events                      v
    v                                     session hub
  host hub                                     |
    |                                          v
    v                                Session.Events
  Host.Events
```

## Design

- The caller gives the `Spec` at each `Start`, and the host keeps no record of it except the agent file. The host then has no agent registry and no index. The cost: after a restart, the caller gives the spec again.
- A spec can name an agent file in its dirs. The host reads only that file, because its model picks the driver, and the driver gets the dirs for the rest. A field that the spec sets wins over the file, so a caller can change one field of an agent.
- A new session with an agent file keeps the agent in the `gopi.host/agent` value before the driver starts, and a resume runs that agent. A crash during the start then cannot leave a session with no agent. An edit of the file then changes only the sessions that start after it, as the model of a harness session stays. A resume with another agent name fails, because one session runs one agent.
- The host writes no entry into the session log, so the model reads the same log that the agent keeps without a host. If the store has a log with the ID of a `Spec`, the driver resumes it.
- One host owns a session. Two hosts on one store must not run the same session, because their entries interleave in one log.
- The host events follow the pattern of the [agent events](../agent/run-stream.md): a sealed interface, one type for each event, and a `State` that applies each event.
- Each `Events` call gives a copy of the state and each event after it, under one lock, so a reader gets no gap and no repeat. `State.Apply` changes the state in place, and the hub clones it for each new reader, because the host applies every event and a reader joins less often. A reader that falls too far behind loses its stream, so one stalled reader cannot hold the events in memory.
- The host stream has no agent events, so a reader of the host does not get the deltas of each session.
- The hub of a session applies each agent event to its own `agent.State`. The state of the runner can be ahead of the events that the worker read, so a copy of it would repeat an event.
- The state of a session stream has no queues, so a reader steers only through `Send`. The host stream then reports each message.
- The messages of a session run one at a time, and a message to a busy session queues by default. A caller names a policy for another rule: steer, interrupt, or reject.
- `Send` returns when the session accepts the message, and `run_end` reports the end of the run. The host then needs no handle for a run.
- A steer goes to the active run only if the runner is an `agent.Steerer`. Otherwise the message queues, so a message to a CLI agent is never lost.
- At the end of each run, the host puts the steer that no run read at the front of the queue, after the messages with Interrupt. A steer can come after the last read of a run, or the run can fail first. An interrupt is the newest request of the caller, so it runs first.
- The host starts and ends a session on the stream under its own lock. No event of a session then comes before its start, and a new session with the same ID comes after the end of the old one.
- The host wraps the approve function of the spec, and the caller owns the wait. With no approve function, the agent denies each call that needs a person, so an agent with no person present fails closed.
- The context of an approval ends when its run ends, and the run publishes the end of each approval that waits before `run_end`. The Codex agent neither ends nor waits for its approvals, so without this an approval stays open after its run. The host drops a later answer, and refuses an approval while no run is active.
- The host gives the mode of the spec to the driver and does not read it, because only the agent knows which calls ask.
- `Close` waits for the run, and the run waits for each approval. So an approve function or a hook that calls `Close` deadlocks.

## Map

| Task                                              | File                   |
| ------------------------------------------------- | ---------------------- |
| Change how a session starts                       | `pkg/host/host.go`     |
| Change how an agent file fills a spec             | `pkg/host/agentfile.go` |
| Change how a session closes, or its agent stream  | `pkg/host/session.go`  |
| Change how a message queues or runs               | `pkg/host/run.go`      |
| Change how an approval reaches the spec           | `pkg/host/approval.go` |
| Change the host events or their JSON              | `pkg/host/event.go`    |
| Change how an event changes the state             | `pkg/host/state.go`    |
| Change how a reader gets the state and the events | `pkg/host/hub.go`      |

## Related Concepts

- [Driver](../agent/driver.md)
- [Definition](../def/definition.md)
- [Catalog](../catalog/catalog.md)
- [Run Stream](../agent/run-stream.md)
- [Log](../session/log.md)
