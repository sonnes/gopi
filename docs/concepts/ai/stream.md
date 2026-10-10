---
title: Stream
description: How one model call reports its events and its final message.
package: pkg/ai, pkg/stream
read_when:
  - You consume the events of a model call
  - You write a provider that produces events
  - A call ends with an error
---

# Stream

A stream is the events of one model call, read in order, followed by one final message. The generic stream in `pkg/stream` connects one producer goroutine to one consumer. The event stream in `pkg/ai` is that stream with an event and a message as its types.

```
provider goroutine                    consumer
  push(start)          -> Events() -> start
  push(text_delta)     ->          -> text_delta
  push(tool_end)       ->          -> tool_end
  return message, err  -> Wait()   -> message, err
```

## Design

- The final message and the terminal error are the result of the stream. Events yields the error as its last item, and Wait returns it. A consumer that breaks out of Events early does not see the error, so it calls Wait.
- `StreamText` is the only entry point for text. A caller that wants only the message calls Wait, so a provider has one code path.
- An event carries the index of its content block. A response holds several blocks, and a consumer that shows thinking next to text needs to know which block a delta belongs to.
- A provider returns no final message when its stream fails. A consumer that keeps the partial message builds it from the events, and each tool end event carries a finished call with its ID. The rules of the agent for that message are in [Run Stream](../agent/run-stream.md).
- An early break out of Events does not stop the producer, because a cancel goes through the context of the call. The response then runs to its end, and the stream drops the later pushes.
- A provider that cannot build a request returns a stream that fails immediately. The caller then has one error path.
- An event round-trips through JSON with its zero fields omitted. An application forwards a stream to a client without a second type.

## Map

| Task                                 | File                     |
| ------------------------------------ | ------------------------ |
| Read the event types                 | `pkg/ai/event.go`        |
| Produce a stream in a provider       | `pkg/ai/stream.go`       |
| Change how a stream delivers events  | `pkg/stream/stream.go`   |

## Related Concepts

- [Message](message.md)
- [Provider](provider.md)
