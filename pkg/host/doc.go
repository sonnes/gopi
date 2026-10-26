// Package host runs sessions of gopi agents for a Go program. A program
// drives each session by its ID: [Host.Create], [Host.Send],
// [Host.Interrupt], and [Host.Confirm]. The model spec of a session picks
// the driver of the catalog: the harness, Claude Code, or Codex.
//
// The host keeps the config and the status of each session in session
// values. A session then keeps its status after a restart, and its agent
// starts again from its config at the next message.
//
// A caller must obey these rules:
//
//   - One host owns each store. Two hosts on one store must not run the
//     same session.
//   - The messages of a session run one at a time.
//   - [Host.Events] has each session and its changes. [Host.Stream] has the
//     agent events of one session. Read each stream once.
//   - A tool call that needs a person waits until [Host.Confirm] or the
//     end of its run. A program with no person confirms from
//     [Host.Events], or picks a mode that asks for no call.
//   - Call Close on the host when it is done. Close stops the process of
//     each CLI agent.
//   - Do not call Close from a hook. Close waits for the run, and the run
//     waits for its hooks.
//
// The reasons are in docs/concepts/host/host.md.
package host
