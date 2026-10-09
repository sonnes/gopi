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

An agent is the loop that runs a model with tools until the work is done. Each run calls the model, runs the tools that the model calls, and adds every message to the history in memory as the message completes.

```
  New(lm, History(saved...))
  Run(input)
    agent_start
    +--> turn_start
    |      the system change, if the sections or the tools changed
    |      the new messages: input, a queue, or BeforeStop
    |      BeforeCall ........ change the request of one call
    |      call the model .... the assistant message
    |      AfterCall ......... change the message before message_end
    |      for each tool call, in call order:
    |        BeforeTool ...... change or block the call
    |      run the calls at the same time, and for each call:
    |        AfterTool ....... change the result
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

- The loop does no store I/O. The history lives in the [State](state.md), and a caller saves each message from the [Run Stream](run-stream.md). So the loop depends on no store contract.
- Each setting of every model call is an option of the agent: `MaxTokens`, `Thinking`, and `System`. A caller then configures an agent from one package, and GoDoc lists every setting in one place. A new text option in `pkg/ai` needs a new option here.
- The tool calls of one message run at the same time, and the loop adds the results in call order after the last call ends. The slowest call then sets the time of the turn, and the history is the same as for calls in order.
- If a message calls a sequential tool, every call of the message runs in order. A sequential tool then never runs next to another call of its message.
- A Go error from a tool, an error from a tool hook, and a call to an unknown tool each become an error result. The run goes on, and the model reads the error and can correct the call. A canceled context stops the run, because the caller asked for it.
- A finished tool call runs, whatever the stop reason of its message. A provider can stop at the output cap after a complete call. If the call does not run, the provider sends an error result for it, and the model never reads the output of the tool.
- Two tools with one name are a bug in the caller, so `New` panics. A deferred tool counts, because a change can add it to a later call.
- `Hook` is one interface with a method for each point, and a hook must embed `BaseHook`. A policy at two points, such as a token budget, is then one value. An unexported method forces the embed, so a new point does not break a hook, and a misspelled method compiles and falls back to `BaseHook`.
- A hook gets a copy of the state that the loop changes. A hook adds messages only through the return value of `AfterTurn` or `BeforeStop`, so every message of a run comes from the loop and has its events.
- With calls at the same time, the `BeforeTool` hooks run one call at a time in call order, before any call runs. A hook that asks a person to approve a call then asks one question at a time.
- The messages of `AfterTurn` do not start a turn. `BeforeStop` is the one hook that continues a run, so one place decides the stop.
- The agent has no cap on the turns of a run. A `BeforeCall` hook that reads `State.Turn` and returns an error is the cap. So the loop has one way to stop early.
- A drain takes every message of a queue. A person who sends three corrections wants the model to read all three before its next call.
- `Steer` and `FollowUp` do not start a run. The agent has no goroutine of its own, and the caller reads the stream of each run.

## Map

| Task                                               | File                    |
| -------------------------------------------------- | ----------------------- |
| Add an option                                      | `pkg/agent/agent.go`    |
| Change when a turn ends or a run stops             | `pkg/agent/loop.go`     |
| Change how the model is called                     | `pkg/agent/call.go`     |
| Change how a tool call runs or its result is built | `pkg/agent/toolcall.go` |
| Add a hook or change its input                     | `pkg/agent/hooks.go`    |
| Change what an event does to the state             | `pkg/agent/state.go`    |
| Change when a queue is drained                     | `pkg/agent/loop.go`     |

## Related Concepts

- [Run Stream](run-stream.md)
- [State](state.md)
- [Tool](../tool/tool.md)
- [Tool Change](tool-change.md)
