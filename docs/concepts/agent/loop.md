---
title: Loop
description: How an agent runs a model with tools until the work is done, and where a hook changes a run.
package: pkg/agent
read_when:
  - You build an agent over a model and tools
  - A run stops earlier or later than you expect
  - You change a model call, a tool call, or the stop of a run from a hook
  - You steer a run that is active
---

# Loop

An agent is the loop that runs a model with tools until the work is done. It stores each message in its [log](../session/log.md) as the message completes, then adds it to the history.

```
  Run(entries...)
    agent_start
    +--> turn_start
    |      the results of interrupted calls, in turn 1
    |      the system change, if any
    |      the new messages: input, a queue, or BeforeStop
    |      BeforeCall
    |      call the model
    |      AfterCall
    |      for each tool call, in call order:
    |        BeforeTool ...... change or block the call
    |      run the calls, and for each call:
    |        AfterTool
    |        store its result, as the call ends
    |    turn_end
    |    AfterTurn ........... add entries, no new turn
    |    steer queue? ........ yes: next turn
    |    tool calls? ......... yes: next turn
    |    follow-up queue? .... yes: next turn
    +--- BeforeStop entries?.. yes: next turn
    agent_end
```

## Design

- Each setting of a model call is an option of the agent, so a caller configures an agent from one package. The cost: a new text option in `pkg/ai` needs a new option here.
- The tool calls of one message run at the same time, so the slowest call sets the time of the turn. If a message calls a sequential tool, every call of the message runs in order.
- A Go error from a tool or a tool hook, or an unknown tool, becomes an error result, so the model can correct the call. A canceled or expired context stops the run, because the caller asked for it.
- A finished tool call runs whatever the stop reason, except a refusal. A provider can stop at the output cap after a complete call, and a call that does not run gives the model no result.
- Each point of a hook is its own interface, such as `BeforeToolHook`, so a policy at two points is one value, and a driver sees the points that a hook uses. The unexported method of `Hook` forces the embed of `BaseHook`. The cost: a misspelled method compiles and never runs.
- A hook adds entries only through the return value of `AfterTurn` or `BeforeStop`, or through `State.Steer` and `State.FollowUp`, so every message of a run has its events. `State.Log` has no `Append`.
- The `BeforeTool` hooks run one call at a time, before the calls that run together start. A hook that asks a person to approve a call then asks one question at a time.
- `BeforeStop` is the one hook that continues a run, so one place decides the stop. The agent has no turn cap, so a run stops early in one way: a `BeforeCall` hook that reads `State.Turn` returns an error.

## Map

| Task                                                        | File                    |
| ----------------------------------------------------------- | ----------------------- |
| Add an option                                               | `pkg/agent/agent.go`    |
| Change when a turn ends, a run stops, or a queue is drained | `pkg/agent/loop.go`     |
| Change how the model is called                              | `pkg/agent/call.go`     |
| Change how a tool call runs or its result is built          | `pkg/agent/toolcall.go` |
| Add a hook or change its input                              | `pkg/agent/hooks.go`    |

## Related Concepts

- [Durability](durability.md)
- [Run Stream](run-stream.md)
- [State](state.md)
- [Log](../session/log.md)
- [Tool](../tool/tool.md)
- [Tool Change](tool-change.md)
