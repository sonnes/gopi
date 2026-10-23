---
title: Value
description: How a session keeps application state next to its entries, and why the model never reads it.
package: pkg/session
read_when:
  - You decide where a title, a model choice, or a label for a session lives
  - You set a value from a hook of an agent
  - You add a value to a store
---

# Value

A value is one piece of application state in a session: a title, a model choice, or a count that a hook keeps. A namespace and a key name it. The newest set of a name wins, and a delete removes it. The model never reads a value.

```
  Value[T]{Namespace, Key}          the name and the type, bound once
     |
     | Set(w, x): encode x as JSON
     v
  store: SetValue(StoredValue{Namespace, Key, Data})
     |
     | Load: the newest Data for each name
     v
  Log: Get: decode Data into T
```

## Design

- Application state lives in the session and not in the storage of the application. A title or a model choice then moves, resumes, and is deleted with its session. The model never reads a value, so a value cannot change a conversation by accident.
- The name and the type are bound once, in a `Value[T]`. A read and a write through one `Value` then cannot disagree about the type.
- `Get`, `Set`, and `Delete` are methods on `Value[T]` and not on `Log`. A Go method cannot take its own type parameter, so a method on `Log` would lose `T`.
- `Set` and `Delete` take a `Writer`, so a hook of an agent sets a value through `State.Log`. A value write sends no event, so it writes to the store directly and does not change the order of the events of a run.
- `Read` decodes a value from a `Snapshot`. A caller that must know a value before it opens the session then reads the store once and does not create the session.
- A store keeps a value as JSON bytes and never decodes it. A store then needs no schema and no list of types.
- A JSON `null` is a set value. Only a delete makes a value absent. A caller can then keep "no choice" apart from "never asked".
- A value and an entry do not change in one step. A crash between an append and a set can keep one of them, and a run that fails after a hook set a value keeps the value.
- The namespace `gopi` and every `gopi.` namespace belong to this module. Nothing checks this at run time, so the rule costs no code on each write.

## Map

| Task                                  | File                         |
| ------------------------------------- | ---------------------------- |
| Change how a value is read or written | `pkg/session/value.go`       |
| Change how a file keeps a value       | `pkg/store/fs/sessions.go`   |

## Related Concepts

- [Log](log.md)
- [Store](store.md)
- [State](../agent/state.md)
