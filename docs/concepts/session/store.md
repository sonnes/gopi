---
title: Store
description: How a session record, its entry log, and its values are kept, and what a store must promise.
package: pkg/session, pkg/session/jsonl
read_when:
  - You implement a store over your own database
  - You open a session that another process wrote
  - A session file ends with a torn line
---

# Store

A store keeps three things for each session: the record, which says that the session exists, the entry log in append order, and the values. A log opens a session through a store and writes to it. The memory store is for tests and for an agent that ends with the process. The JSONL store is one append-only file per session.

```
  Open(store, "user-42")
    |
    +-- Load  -> not found -> Create the record, empty log
    |
    +-- Load  -> snapshot  -> record, entries in order, current values
```

## Design

- A store has these methods: create, load, append, set a value, and delete a value. That is the whole contract that a log needs, so a new database needs little code.
- The record holds an ID and a creation time, and nothing from the application. A title, a mode, or a model choice goes in a value, for the reasons in [Value](value.md).
- The caller fills every field of the record, the creation time included. A store has no clock of its own, so two stores write the same bytes for the same record.
- Load returns the record, the entries, and the values in one call. Open needs all of them, and one method is one round trip. Open creates or resumes, because both paths give the same ready log.
- The JSONL store keeps one file per session, and the first line is the record. A complete line that does not decode is an error, because a skipped line would lose history.
- A crash in the middle of a write leaves a torn last line with no newline, and Load cuts it from the file. A whole last line with no newline gets its newline, so the next append starts on a new line. No fsync runs, so a crash of the OS can lose the last lines.
- Load changes the end of the file, so only the writer of a session calls Load, or a process that runs when no writer runs. A Load in a second process during a write can cut a line that is not torn.
- A failed append cuts the file back to its size before the write. A partial line would join the next line, and Load rejects a complete line that does not decode.
- A torn record is a create that a crash stopped, so Load reports the session as not found, and Create replaces the file. A create that fails removes its file, so a later create can use the ID.
- Each line has a `type` field, and the other fields sit flat next to it. The first line has the type `session_init`, an entry line has the type `message`, and a value line has the type `value`. A custom entry has the type `message` too, so a file of an older version loads as it is.
- A value line is a write, not the state. Load replays the value lines in order, so the file never rewrites a line. The cost is that a value that changes often makes the file grow.

## Map

| Task                                         | File                         |
| -------------------------------------------- | ---------------------------- |
| Implement a store                            | `pkg/session/store.go`       |
| See a store in memory                        | `pkg/session/memstore.go`    |
| Change the file format                       | `pkg/session/jsonl/store.go` |
| Change where the CLI keeps the session files | `cmd/gopi/main.go`           |

## Related Concepts

- [Log](log.md)
- [Value](value.md)
