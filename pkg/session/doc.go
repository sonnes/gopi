// Package session is the stored history of an agent. A [Session] record
// says that a session exists. An [Entry] is one message of the session,
// or one custom entry: a fact of the application in its place in the
// log, such as the summary of a compaction. A [Value] is one piece of
// application state in the session. A [Storer] keeps all three. A [Log]
// is the live handle that an agent reads and writes. [Log.Writer] gives
// a [Writer] that reads the log and sets values, and cannot append.
//
// A caller must obey these rules:
//
//   - The application chooses the session ID. The package never makes
//     one. An ID that the package made would live only inside one
//     process, so the application cannot store it or resume it.
//   - The log is append-only. [Log.Append] adds entries. Nothing changes
//     or removes an entry. The model reads the entries in append order.
//   - A session has one writer. Two logs on one session interleave
//     their entries.
//   - The log sets ID and CreatedAt on an entry. A caller sets Author and
//     Ephemeral, with the options of [Text], [NewEntry], and [Custom].
//   - An ephemeral entry never reaches the store. The next run does not
//     read it. Use it for context that is true only now.
//   - The model never reads a value or a custom entry. They are for
//     the application.
//
// The reasons are in docs/concepts/session/log.md,
// docs/concepts/session/value.md, and docs/concepts/session/store.md.
package session
