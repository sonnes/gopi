// Package host runs sessions of gopi agents for a Go program. The model
// spec of a session picks the driver of the catalog: the harness,
// Claude Code, or Codex.
//
// A caller must obey these rules:
//
//   - One host owns each session. Two hosts on one store must not run
//     the same session.
//   - The messages of a session run one at a time.
//   - [Host.Events] has the state of each session. [Session.Events] has
//     the agent events of one session. Read each stream once.
//   - Call Close on the host when it is done. Close stops the process of
//     each CLI agent.
//   - Do not call Close from an approve function or a hook. Close waits
//     for the run, and the run waits for them.
//
// The reasons are in docs/concepts/host/host.md.
package host
