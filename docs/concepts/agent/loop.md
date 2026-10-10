---
title: Loop
description: How an agent runs a model with tools until the work is done, and where its history lives.
package: pkg/agent
read_when:
  - You build an agent over a model and tools
  - You save a conversation and give it to an agent again
  - A run stops earlier or later than you expect
  - You change a model call, a tool call, or the stop of a run from outside the loop
  - You send a message to an agent while a run is active
---

# Loop

An agent is the loop that runs a model with tools until the work is done. It adds each message to the history as the message completes.

```
  New(lm, History(saved...))
  Run(input)
    agent_start
    +--> turn_start
    |      the system change, if the sections or the tools changed
    |      the new messages: input, a queue, or BeforeStop
    |      BeforeCall
    |      call the model .... the assistant message
    |      AfterCall ......... before message_end
    |      for each tool call, in call order:
    |        BeforeTool ...... change or block the call
    |      run the calls at the same time, and for each call:
    |        AfterTool
    |      add the results, in call order
    |    turn_end
    |    AfterTurn ........... add messages, no new turn
    |    steer queue? ........ yes: next turn
    |    tool calls? ......... yes: next turn
    |    follow-up queue? .... yes: next turn
    +--- BeforeStop messages?  yes: next turn
    agent_end
```

## Design

- The loop does no store I/O, so it depends on no store contract. The history lives in the [State](state.md), and a caller saves each message from the [Run Stream](run-stream.md).
- Each setting of a model call is an option of the agent, so a caller configures an agent from one package. The cost: a new text option in `pkg/ai` needs a new option here.
- The tool calls of one message run at the same time, so the slowest call sets the time of the turn. If a message calls a sequential tool, every call of the message runs in order.
- A Go error from a tool or a tool hook, or an unknown tool, becomes an error result, so the model can correct the call. A canceled or expired context stops the run, because the caller asked for it.
- A cancel that stops one call drops the results of every call that ran with it, so the model never reads their output. A provider then sends an error result for each call with no result, as in [Message](../ai/message.md).
- A finished tool call runs whatever the stop reason of its message, except a refusal. A provider can stop at the output cap after a complete call, and a call that does not run gives the model no result.
- `Hook` is one interface with a method for each point, so a policy at two points, such as a token budget, is one value. An unexported method forces the embed of `BaseHook`. The cost: a misspelled method compiles and falls back to `BaseHook`.
- A hook adds messages only through the return value of `AfterTurn` or `BeforeStop`, so every message of a run has its events.
- The `BeforeTool` hooks run one call at a time, before any call runs. A hook that asks a person to approve a call then asks one question at a time.
- `BeforeStop` is the one hook that continues a run, so one place decides the stop. The agent has no turn cap, so a run stops early in one way: a `BeforeCall` hook that reads `State.Turn` returns an error.

## Map

| Task                                                        | File                    |
| ----------------------------------------------------------- | ----------------------- |
| Add an option                                               | `pkg/agent/agent.go`    |
| Change when a turn ends, a run stops, or a queue is drained | `pkg/agent/loop.go`     |
| Change how the model is called                              | `pkg/agent/call.go`     |
| Change how a tool call runs or its result is built          | `pkg/agent/toolcall.go` |
| Add a hook or change its input                              | `pkg/agent/hooks.go`    |
| Change what an event does to the state                      | `pkg/agent/state.go`    |

## Related Concepts

- [Run Stream](run-stream.md)
- [State](state.md)
- [Tool](../tool/tool.md)
- [Tool Change](tool-change.md)
