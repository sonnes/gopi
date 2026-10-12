---
title: Log
description: How a session keeps its history as an append-only list of entries that the model reads in order.
package: pkg/session
read_when:
  - You decide what an agent reads before a model call
  - You store a fact of the application in its place in a conversation
  - A session has a tool call with no result after a crash
---

# Log

A log is the live handle on one session. It holds the record of the session, its entries in append order, and its values. The model reads the messages of the entries in the same order.

```
  store (one session)            Log

  record   {ID, CreatedAt}       Session()
  entries  e1 e2 c3 e4           Entries()  = e1 e2 c3 e4
                                 Messages() = e1 e2 e4, repaired
                                 Append(e5) -> e5 is last
  values   app.title = "..."     Value[T].Get / Set / Delete

  c3 is a custom entry
```

## Design

- A session is a list of entries. An edit or a retry is a new session that holds the entries that the application keeps.
- A session has one writer. Two logs on one session interleave their entries, and the result can be a conversation that a provider rejects. A lock across processes belongs to the application that runs them.
- An ephemeral entry gets an ID and reaches the model in its run, but the store never sees it and the log does not keep it. It is context that is true only now.
- A custom entry has a kind and JSON data, and no message. The log keeps it in its place among the messages, and `Messages` skips it. An application then stores a fact that it reads back in order, such as the summary of a compaction, and the model never reads it.
- The log repairs a tool call with no result when it builds the messages. The call is the footprint of a crash, and a provider rejects it. The repair lives in the log because the fact is about the stored entries, not about the loop that reads them.
- `Author`, `Ephemeral`, and `Kind` live on the entry and not on the message. A provider never reads them. They are facts about the application and the store, so they stay out of `pkg/ai`.
- `Writer` is a `Reader` that also sets values, and `Log.Writer` returns one with no `Append`. A hook of an agent gets that `Writer`, so every entry of a run still comes from the loop, which sends its events in order. The unexported methods of both interfaces keep the implementations in this package.
- The application chooses the session ID and the log never makes one. An ID that the package made would live only inside one process, so the application could not store it or resume it.

## Map

| Task                                             | File                   |
| ------------------------------------------------ | ---------------------- |
| Change what the model reads from the log         | `pkg/session/log.go`   |
| Build an entry with an author, ephemeral, or custom | `pkg/session/entry.go` |

## Related Concepts

- [Value](value.md)
- [Store](store.md)
- [Message](../ai/message.md)
- [Loop](../agent/loop.md)
