---
title: CLI Agent
description: How an agent CLI such as Codex or Claude Code runs as a gopi agent, with the same events, state, and log as the loop.
package: pkg/agent/codex, pkg/agent/claude, pkg/agent/internal/cliproc, pkg/agent/internal/emit, pkg/agent/internal/emittest, pkg/agent/internal/resume, pkg/agent/internal/tail
read_when:
  - You run Codex or Claude Code from Go
  - You add another agent CLI
  - The log of a CLI agent does not match what the CLI did
  - You answer the permission prompts of Claude Code from Go
  - You answer the approval requests of Codex from Go
  - A CLI agent reports usage that you do not expect
  - You steer a run of Claude Code
---

# CLI Agent

A CLI agent runs an agent CLI as a gopi agent. The CLI calls the model and runs the tools in its own loop. The CLI agent sends the input of a run to the CLI and reads the output. It turns the output into the events of a run stream, the entries of a log, and a [State](state.md).

```
  Run(entries)
    |
    CLI agent ---- input line ------> CLI process (one per agent)
    |                                   its own loop and tools
    |         <--- output lines ------+
    emit.Emitter
    |-- entries --> log          (a mirror of the CLI transcript)
    |-- events ---> State.Apply, then the run stream
```

## Design

- A CLI agent is an agent. It cannot be a provider, because the CLI runs tools and does not answer one model call. It has the same `Run`, `Log`, `State`, and run stream as `agent.Agent`, so a consumer needs no second path.
- Each CLI has its own package and its own type. A [driver](driver.md) starts each agent, and the caller runs it through `agent.Runner`, the methods that the agents share. An option that both packages have has the same name in both.
- The CLI owns the transcript. The log is a mirror for display and replay, and the model never reads it.
- The ID of the CLI conversation, a Codex thread or a Claude Code session, is a session value, so `Durable` resumes the same conversation. Each agent reads and writes it only through `internal/resume`, and a caller never sees it.
- If the CLI cannot find the stored conversation, the run fails with `ErrNoConversation`, and the agent clears the ID. The next run starts a new conversation of the CLI. The error tells the caller that the CLI lost the history, and the log keeps its messages.
- The emitter keeps the event order of the loop. The input goes in turn 1, after `turn_start`. The log stores each message before its `message_end`, and every run that starts ends with `agent_end`, which carries the error of a failed run.
- A tool call of the CLI becomes a tool call in an assistant message, then a tool result message. `Log.Messages` then stays valid, and a consumer shows a CLI tool the same way as a gopi tool.
- If a run fails with a tool call open, the call gets the stored result of `session.Interrupted`. The log and the state then have no call without a result.
- If a run fails or stops during an assistant message, the log stores the blocks that the CLI completed, because the CLI transcript keeps them. The block in flight is not stored. If no block completed, the `message_end` has no content.
- The input of a run is the text and the files of its entries, in order. A file that the CLI does not read becomes a text note, so the model knows that the file was there. The run does not fail, because the text can still answer.
- An ephemeral entry goes to the CLI, and the log does not keep it. The CLI keeps it in its own transcript, so a later run of the CLI still reads it.
- The CLI runs only its own tools. A CLI agent has no `Toolset` option, and a gopi tool does not reach the CLI.
- One process lives as long as the agent. The first run starts it, and `Close` stops it. A process per run pays the start and the handshake on each run.
- The packages speak the protocols of the IDE extensions: `codex app-server` and the stream-json mode of `claude`. The one-shot modes cannot interrupt a turn or answer a request of the CLI.
- A cancel interrupts the turn and waits for the end of the turn. The agent kills the process only after a timeout. The next run then starts from a known state.
- The Claude Code agent is an `agent.Steerer`. A steer goes to the CLI at once as a user line with the priority `now` and a UUID. The CLI ends the segment that runs with a result line whose terminal reason is `aborted_tools` or `aborted_streaming`, and a new segment takes the steer. That result line does not end the run. A tool that runs in the foreground ends first, and the steer takes effect before the next model call.
- The agent starts the CLI with `--replay-user-messages`, so the CLI echoes the UUID of a steer when it takes it. The log stores the steer at the echo, which is the point where the model reads it.
- A steer cancels a subagent that runs in the foreground, so the agent moves each open subagent call to the background with a `background_tasks` request before the steer.
- If a run ends before the CLI takes a steer, the agent stops the CLI, so the CLI does not run the steer after the run. `TakeSteer` returns the steer, and the host runs it next. Codex does not steer, so the host holds a message for Codex until the run ends.
- Both agents take the same approval function as the harness, `agent.ApproveFunc`, so one function answers the approvals of each agent. The function gets the tool call as the log has it, so a rule for the log matches the approval too. The cost: Codex cannot change the input of an action or send a reason to the model, so it declines a changed input and drops the reason.
- The agent answers every request of the CLI, and declines an approval without an `Approve` function. Each unknown request gets an error. The CLI waits for an answer, so a request with no answer stops the turn.
- The Codex approve function runs in its own goroutine, so the reader goes on while a caller decides.
- Claude Code calls the gopi hooks over the control protocol. The agent registers one callback for each hook event in `initialize`, and answers each `hook_callback` with the hooks in order. The CLI then runs no command and needs no trust for a hook.
- Codex has no hook callback over its protocol. The agent adds a command hook to the thread through the config of `thread/start`, with the hook trust bypassed, and the command `gopi hook` relays each call to a socket of the agent. The cost: if the command fails or times out, Codex runs the tool. So the agent finds the command before it starts the process, and a run with hooks fails if the command is missing. If the command cannot reach the agent, it fails closed in the shape of each event: a deny, an error output, or the end of the turn.
- A hook of a CLI agent gives text entries only, because a CLI takes only text context from a hook. A CLI has no hook at a model call, so its driver rejects `BeforeCall` and `AfterCall`.
- Both agents start, read, and stop the process of the CLI through `internal/cliproc`. A stop closes stdin, sends SIGTERM after a grace period, and kills the process after a timeout. It reads stdout until the process exits, so the CLI does not block on a full pipe.
- A CLI that is not logged in fails the run with `ErrAuth`, so a caller can ask the user to log in. Claude Code reports a login failure only as text in its result, so the agent matches that text. Codex marks the failed turn as unauthorized.
- The usage of a Claude run is the sum of the usage of its result lines: one line for each segment that a steer ended, and the last line. A run that fails before its result reports the sum of the messages that it ended, so `agent_end` does not erase the usage of the run. Codex sends a token update after each model request, and it can send the same update again. The agent counts an update only when the total of the thread changes.
- The agent keeps the end of the stderr of the CLI and puts it in the error when the process exits. The CLI writes the reason for a failure at start there, such as a bad flag. The agent does not copy stderr to the terminal, because a TUI owns the terminal.
- The tests replay the stdio of the CLI from a trace file with `internal/stdiorr`. Each client sends deterministic request IDs, so a trace matches on every run. `make record-cli` records a trace from the real CLI.

## Map

| Task                                        | File                              |
| ------------------------------------------- | --------------------------------- |
| Change how CLI output becomes events        | `pkg/agent/internal/emit/emit.go` |
| Change which input blocks go to a CLI       | `pkg/agent/internal/emit/emit.go` |
| Change how a file becomes a Claude block    | `pkg/agent/claude/run.go`         |
| Change how a file becomes a Codex input     | `pkg/agent/codex/codex.go`        |
| Change the event rules that the tests check | `pkg/agent/internal/emittest/check.go` |
| Change how a Codex item becomes a message   | `pkg/agent/codex/turn.go`         |
| Change the Codex handshake or a request     | `pkg/agent/codex/conn.go`         |
| Change how a Claude line becomes a message  | `pkg/agent/claude/run.go`         |
| Change a control request of Claude Code     | `pkg/agent/claude/process.go`     |
| Change how a steer reaches Claude Code      | `pkg/agent/claude/steer.go`       |
| Change how a CLI process starts or stops    | `pkg/agent/internal/cliproc/cliproc.go` |
| Change where a CLI keeps its conversation ID | `pkg/agent/internal/resume/resume.go` |
| Record or replay a trace                    | `internal/stdiorr/rr.go`          |
| Change how stderr reaches an error          | `pkg/agent/internal/tail/tail.go` |

## Related Concepts

- [Loop](loop.md)
- [Run Stream](run-stream.md)
- [State](state.md)
- [Durability](durability.md)
- [Log](../session/log.md)
- [Value](../session/value.md)
