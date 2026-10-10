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

- Each event is one change to the state of the agent. The stream sends the events, also those of tool calls at the same time, in the order that the state applied them. The reasons are in [State](state.md).
- Each message of the history arrives as one `message_end`, so a caller saves a conversation from the stream. The run waits for the consumer only when the stream buffer is full, so a crash loses the messages in the buffer. If the consumer stops reading, the run goes on and the stream drops the later events, so the save misses their messages.
- The new messages of a turn, with the input of the run, come after its `turn_start` as message events. A viewer that has only the stream then sees every message that the run adds to the history. The system change comes first in the turn, for the reason in [Section](section.md).
- On every exit, the loop sends the end event of each open scope, then `agent_end` with the error of a failed run. A failed run then fails the stream with the same error.
- Runs on one agent are sequential, so two runs never add to one history at the same time. A `Run` while another run is active does not start, and its stream fails with no event.
- A failure that cuts an assistant message short sends its `message_end` with the part that arrived, without the tool calls that did not finish. Its stop reason is `error`, or `aborted` if the caller canceled. If the part has content, the history keeps it as a record of what the model said.
- Each `tool_execution_end` comes as its call ends, so a consumer can show a call as done before the others. With calls in order, each call sends its start, its end, and its result before the next call starts.
- `agent_end` carries the usage and no messages. The state and the result of the stream already have the messages.

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
