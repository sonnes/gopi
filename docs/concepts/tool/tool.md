---
title: Tool
description: How a tool is defined, deferred, run, and wrapped by middleware.
package: pkg/tool
read_when:
  - You define a tool for a model
  - You defer a tool until the model needs it
  - You wrap tools with a rule, such as a deny list or a reminder
  - A tool fails and you decide what the model sees
---

# Tool

A tool is one function that a model can call. It has info that the model reads and a run function that the agent calls with a tool call. A middleware is a function that takes a tool and returns a tool.

```
Chain(t, a, b)

  call   -> a -> b -> t
  result <- a <- b <- t
```

## Design

- A tool is in its own package, and `pkg/ai` keeps the info, the call, and the result. A provider then does not depend on the code that runs a tool. The result stays in `pkg/ai`, because its JSON uses the content codec there.
- The deferred flag and the sequential flag are fields of the info with no JSON, so they never reach the model or the log. A middleware that builds on `Wrap` passes the info on, so the wrapped tool keeps its schema and its flags. A middleware that returns its own type of tool loses the flags if it does not copy the info.
- An error result is a failure that the model reads and can correct. A Go error is a failure of the run function, such as a transport that is down, and a retry middleware acts on it. [Loop](../agent/loop.md) has what the agent does with a Go error that is left.
- A middleware receives the context and the call and nothing else, so `pkg/tool` holds no agent concept. A layer with more state closes over the state or puts it in the context.
- A middleware wraps one tool and goes with it to any agent. A `BeforeTool` or `AfterTool` hook covers every tool of the agent, `tool_search` too, and reads the state of the agent. A rule of one tool goes in a middleware, and a rule for every tool goes in a hook.

## Map

| Task                             | File                |
| -------------------------------- | ------------------- |
| Define a tool with a typed input | `pkg/tool/tool.go`  |
| Write a middleware               | `pkg/tool/tool.go`  |
| Change the info or the result    | `pkg/ai/tool.go`    |
| Turn a result into a message     | `pkg/ai/message.go` |
| Send tools to a model            | `pkg/ai/options.go` |

## Related Concepts

- [Message](../ai/message.md)
- [Provider](../ai/provider.md)
