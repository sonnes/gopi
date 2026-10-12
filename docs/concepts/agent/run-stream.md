---
title: Run Stream
description: How one run reports its events in order, and what a message_end promises about the log.
package: pkg/agent
read_when:
  - You consume the events of a run
  - You show what the log of a run holds
  - A run fails and you need to know what the stream sends
---

# Run Stream

A run stream is the events of one run, read in order, followed by the messages that the run produced. It is the generic stream of `pkg/stream`, with an agent event and a message list as its types.

```
agent_start
turn_start                        the turn, from 1
  message_start, message_end ...  interrupted results, in turn 1
  message_start, message_end      the system change, if any
  message_start, message_end ...  the new messages of the turn
  message_start
  message_update ...
  message_end                     the assistant message
  tool_execution_start ...        each call, in call order
  tool_execution_end,             as each call ends
    message_start, message_end    its result
turn_end                          the assistant message, the results
message_start, message_end ...    the message entries of AfterTurn hooks
... while a tool call or a message continues the run ...
agent_end                         the usage, and the error if it failed
```

## Design

- Each event is one change to the state of the agent. The stream sends the events, also those of tool calls at the same time, in the order that the state applied them, for the reasons in [State](state.md).
- The log stores each message before its `message_end`, so a consumer that reads a `message_end` knows that the store has the message. An ephemeral message has the `Ephemeral` mark, because the store does not have it. A custom entry is stored with no event, because the model never reads it.
- The run waits for the consumer only when the stream buffer is full. If the consumer breaks out of the events or calls `Wait`, the run goes on and the stream drops the later events. The log still has their messages.
- The new messages of a turn, with the input of the run, come after its `turn_start` as message events. A viewer that has only the stream then sees every message that the run adds to the history. The system change comes before the new messages, for the reason in [Section](section.md).
- On every exit, the loop sends the end event of each open scope, then `agent_end` with the error of a failed run. A failed run then fails the stream with the same error.
- Runs on one agent are sequential, so two runs never add to one log at the same time. A `Run` while another run is active does not start, and its stream fails with no event.
- A failure that cuts an assistant message short sends its `message_end` with the part that arrived, without the tool calls that did not finish. Its stop reason is `error`, or `aborted` if the caller canceled. If the part has content, the log keeps it.
- Each `tool_execution_end` comes as its call ends, and its result comes after it, so a consumer can show a call as done before the others. With calls in order, each call sends its start, its end, and its result before the next call starts.
- `agent_end` carries the usage and no messages, because the state and the result of the stream have them.

## Map

| Task                                | File                   |
| ----------------------------------- | ---------------------- |
| Read the event types                | `pkg/agent/event.go`   |
| Change when an event is sent        | `pkg/agent/loop.go`    |
| Change how a stream delivers events | `pkg/stream/stream.go` |

## Related Concepts

- [Loop](loop.md)
- [State](state.md)
- [Log](../session/log.md)
- [Stream](../ai/stream.md)
