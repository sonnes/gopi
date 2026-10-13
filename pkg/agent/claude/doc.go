// Package claude runs the Claude Code CLI as a gopi agent. The CLI runs
// its own loop and its own tools. An [Agent] sends the input of each
// run to the CLI and turns the output into the same [agent.Event]
// values as [agent.Agent]. It writes every message to a
// [session.Log].
//
// Each event of a run changes the state of the agent before the
// stream gets it, as in [agent.Agent]. [Agent.State] returns a copy.
//
// The agent speaks the stream-json protocol of the CLI on stdin and
// stdout, with the control channel for the initialize, interrupt, and
// permission requests.
//
// A caller must obey these rules:
//
//   - The CLI must be installed and logged in. [Binary] sets its path.
//     A run of a CLI that is not logged in fails with [ErrAuth].
//   - Runs on one agent are sequential. A second Run fails on its
//     stream.
//   - The first run starts the CLI, and it runs until [Agent.Close].
//     Call Close when the agent is no longer needed.
//   - Without [CanUseTool], the agent denies every permission prompt of
//     the CLI. Set [CanUseTool], a [PermissionMode], or [AllowedTools]
//     to let the CLI run the tools.
//   - The text, the images, and the PDFs of the input entries go to the
//     CLI. Each other file becomes a text note.
//   - The log holds the messages of the main agent. The messages of a
//     subagent of the CLI are part of its tool call, and the log does
//     not hold them.
//
// The reasons are in docs/concepts/agent/cli-agent.md.
package claude
