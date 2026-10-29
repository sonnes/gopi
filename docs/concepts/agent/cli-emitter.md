---
title: CLI Emitter
description: How the output of an agent CLI becomes the events of a run stream, the entries of a log, and a state, in the event order of the loop.
package: pkg/agent/internal/emit, pkg/agent/internal/emittest, pkg/agent/claude, pkg/agent/codex
read_when:
  - The log of a CLI agent does not match what the CLI did
  - A CLI run sends its events in an order that you do not expect
  - You change which input blocks go to a CLI
---

# CLI Emitter

The emitter of a [CLI agent](cli-agent.md) turns the output of the CLI into the events of a run stream and the entries of a log. Each event changes a [State](state.md) before the stream gets it. The log is a mirror of the CLI conversation, for display and replay, and the model never reads it.

## Design

- The emitter keeps the event order of the [loop](loop.md). The input goes in turn 1, the log stores each message before its `message_end`, and every run that starts ends with `agent_end`.
- A tool call of the CLI becomes a tool call in an assistant message, then a tool result message, so a consumer shows a CLI tool as it shows a gopi tool. If a run fails with a tool call open, the call gets the result of `session.Interrupted`.
- If a run fails or stops during an assistant message, the log stores the blocks that the CLI completed, because the CLI conversation keeps them. The block in flight is not stored.
- A file of the input that the CLI does not read becomes a text note, so the model knows that the file was there. The run does not fail, because the text can still answer.
- An ephemeral entry goes to the CLI, and the log does not keep it. The CLI conversation keeps it, so a later run still reads it.
- A run that fails before its result reports the usage of the messages that it ended, so `agent_end` does not erase the usage of the run.

## Map

| Task                                        | File                                   |
| ------------------------------------------- | -------------------------------------- |
| Change how CLI output becomes events        | `pkg/agent/internal/emit/emit.go`      |
| Change which input blocks go to a CLI       | `pkg/agent/internal/emit/emit.go`      |
| Change the event rules that the tests check | `pkg/agent/internal/emittest/check.go` |
| Change how a Claude line becomes a message  | `pkg/agent/claude/run.go`              |
| Change how a Codex item becomes a message   | `pkg/agent/codex/turn.go`              |
| Change how a file becomes a Claude block    | `pkg/agent/claude/run.go`              |
| Change how a file becomes a Codex input     | `pkg/agent/codex/codex.go`             |

## Related Concepts

- [CLI Agent](cli-agent.md)
- [Loop](loop.md)
- [Run Stream](run-stream.md)
- [State](state.md)
- [Log](../session/log.md)
