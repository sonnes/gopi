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

The state of an agent is what it knows: the messages that the model reads, the turn, the partial message, the pending tool calls, the usage, the error of the last run, the queues, and the loaded tools. The messages start with the history that `New` gets. After that, only an event changes the messages and the progress of a run.

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

- Every change to the messages and the progress is an event, and `Apply` is the one function that makes it. A viewer that applies the same events to a snapshot then gets the same state as the agent, also across runs. A test runs the scripted runs and compares the two.
- The history from `New` has no event, so a run does not send the whole conversation to each viewer. A viewer starts from a snapshot of the state.
- A hook gets the state. A hook then reads what the model reads, with the pending calls. A hook that needs application state keeps it in its own value, because the agent has no store.
- A caller must not change a message of a copy of the state. The copy shares the message with the agent. The rule is in the doc comment of `State`, because Go has no read-only slice.
- The state reads the tools and the sections from the messages: the base, then the later system messages. So no event changes them, and a viewer gets them from its snapshot.
- An assistant message with no content does not go into the messages, because a provider rejects it. A failure can end a message before its first block.
- The state encodes to JSON without the queues and the loaded tools, because they change with no event. A viewer in another process starts from a snapshot that the agent sends, then applies the events.

## Map

| Task                                   | File                 |
| -------------------------------------- | -------------------- |
| Change what an event does to the state | `pkg/agent/state.go` |
| Change how the loop sends an event     | `pkg/agent/agent.go` |
| Change what a hook reads               | `pkg/agent/hooks.go` |

## Related Concepts

- [Run Stream](run-stream.md)
- [Loop](loop.md)
