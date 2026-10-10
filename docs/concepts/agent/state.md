---
title: State
description: How an agent keeps what it knows in one value that only events change, so a viewer and a hook read the same state as the loop.
package: pkg/agent
read_when:
  - You show the progress of a run in a UI
  - You read the state of an agent from a hook
  - You send the events of a run to another process
  - You add an event type
---

# State

The state of an agent is what it knows: the messages that the model reads, the progress of the run, the queues, and the loaded tools. The messages start with the history that `New` gets. After that, only an event changes the messages and the progress of a run.

```
  Run
   |
   emit(event)
     |
     state.Apply(event) ... under the lock of the agent
     push(event) .......... to the run stream
                               |
                               v
                     viewer: snapshot.Apply(event)
```

## Design

- `Apply` is the one function that changes the messages and the progress, and each change is an event. A test proves that a viewer gets the same state as the agent when it applies every event of each run to a snapshot. If the viewer stops reading a stream, the stream drops the later events, and the viewer misses their changes.
- The history from `New` has no event, so a run does not send the whole conversation to each viewer. A viewer starts from a snapshot that it takes when no run is active. A snapshot from a run holds events that the stream has not delivered, so the viewer applies them two times.
- A hook gets the state, so it reads what the model reads, with the pending calls. If a `BeforeCall` hook changes the messages of a request, the model reads messages that the state does not hold. A hook keeps application state in its own value, because the agent has no store.
- A copy of the state shares its messages with the agent, so a copy costs no allocation. The cost is that a caller must not change a message of a copy. The rule is in the doc comment of `State`, because Go has no read-only slice.
- The state reads the tools and the sections from the messages, so no event changes them, and a saved history replays the same tools and sections. If the saved history lost a system message, it replays other tools and sections. The next run adds a change only if the new agent wants other tools or sections.
- An assistant message with no content does not go into the messages, because a provider rejects it. A failure can end a message before its first block.
- The JSON of the state leaves out the queues and the loaded tools, because they change with no event. A viewer in another process gets its snapshot as JSON, so it never sees the queues or the loaded tools.

## Map

| Task                                   | File                 |
| -------------------------------------- | -------------------- |
| Change what an event does to the state | `pkg/agent/state.go` |
| Change how the loop sends an event     | `pkg/agent/agent.go` |
| Change what a hook reads               | `pkg/agent/hooks.go` |

## Related Concepts

- [Run Stream](run-stream.md)
- [Loop](loop.md)
