---
title: Run Stream
description: How one run reports its events in order, and how a caller saves the messages of a run.
package: pkg/agent
read_when:
  - You consume the events of a run
  - You save the messages of a run
  - A run fails and you need to know what the stream sends
---

# Run Stream

A run stream is the events of one run, read in order, followed by the messages that the run produced. It is the generic stream of `pkg/stream`, with an agent event and a message list as its types.

```
agent_start
turn_start                        the turn, from 1
  message_start, message_end      the system change, if any
  message_start, message_end ...  the new messages of the turn
  message_start
  message_update ...
  message_end                     the assistant message
  tool_execution_start ...        each call, in call order
  tool_execution_end ...          as each call ends
  message_start, message_end ...  each result, in call order
turn_end                          the assistant message, the results
message_start, message_end ...    the messages of AfterTurn hooks
... while a tool call or a message continues the run ...
agent_end                         the usage, and the error if it failed
```

## Design

- Each event is one change to the state of the agent. The loop applies the event to the state, then sends it. The reasons are in [State](state.md).
- Each message of the history arrives as one `message_end`, so a caller saves a conversation from the stream. The agent stays a loop in memory with no store. The run does not wait for the save, so a crash can lose the messages that the stream buffers.
- The new messages of a turn come after its `turn_start`, and the system change comes first in the turn. So the loop has one place for a system change.
- Each `message_start` has a `message_end`. A provider can return a message with no events, and the loop still sends its start.
- The input crosses the stream as message events. A viewer that has only the stream then sees every message that the run adds to the history.
- Each scope of a run has a start event and an end event: `agent`, `turn`, `message`, and `tool_execution`. `agent_end` arrives on every exit of a run that started. On a failure, the loop ends each open scope and sends `agent_end` with the error, then the stream fails with the error.
- A failure that cuts an assistant message short sends its `message_end` with the part that arrived, without the tool calls that did not finish. Its stop reason is `error`, or `aborted` if the caller canceled. If the part has content, the history keeps it as a record of what the model said.
- With calls at the same time, every `tool_execution_start` comes first, in call order. Each `tool_execution_end` comes as its call ends, so a consumer can show a call as done before the others. With calls in order, each call sends its start, its end, and its result before the next call starts.
- `agent_end` carries the usage and no messages. The state and the result of the stream already have the messages.
- Each event is its own Go type, and `Event` is a sealed interface, as `ai.Content` is. A consumer then reads only the fields that the event carries.
- Each event type encodes a `type` field in JSON. Go cannot decode JSON into an interface, so `UnmarshalEvent` reads the field and returns the event of that type.

## Map

| Task                                | File                   |
| ----------------------------------- | ---------------------- |
| Read the event types                | `pkg/agent/event.go`   |
| Change when an event is sent        | `pkg/agent/loop.go`    |
| Change how a stream delivers events | `pkg/stream/stream.go` |

## Related Concepts

- [Loop](loop.md)
- [State](state.md)
- [Stream](../ai/stream.md)
