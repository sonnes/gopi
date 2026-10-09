---
title: Tool Change
description: How an agent loads a deferred tool on demand, and how it adds and removes the tools that the model can call in a conversation.
package: pkg/ai, pkg/tool, pkg/agent
read_when:
  - You give an agent more tools than a model call can hold
  - A saved history has more or fewer tools than the agent declares
  - A provider sends a tool list that you do not expect
---

# Tool Change

A tool change adds tools to the tools that the model can call, or removes tools, from a place in the conversation. A deferred tool is a tool that the agent can run but does not send, until a change adds it. The search tool of the agent loads a deferred tool.

```
  Toolset   not deferred: in the base of a new history
            deferred: known to the agent, not sent
            tool_search: added by the agent, if a tool is deferred
     |
  tool_search loads tools by name or by keyword
     |
  next model call: tools of the history != wanted tools?
     | yes
     v
  system message: ToolRemoved..., ToolAdded...  --> history
     |
  provider: the tools of the base as the tool list
     anthropic    tool_removal, tool_addition blocks, in place
     openai       additional_tools item, in place
                  after a removal: allowed_tools tool choice
                  after a replacement: current tools
     openaichat   every declared tool
                  after a removal: allowed_tools tool choice
                  after an addition or a replacement: a new list
     no form      current tools
```

## Design

- A change is a system message with `ToolAdded` and `ToolRemoved` blocks, and the agent sends the tools of the base as `ai.Tools`. A deploy that changes a tool then adds a change in place, so the cache of a saved history holds.
- The history declares the tools that the model can call, and `State.Tools` reads them. Before each model call, the agent compares them with the wanted tools and adds one system message with the difference. The search keeps the loaded tools in the agent and does not write to the history.
- The agent and each provider fold the tools with `transcript.CurrentTools`, so they agree on the tools at each place. The fold is in `internal/transcript`, because `pkg/ai` holds no conversation state.
- The agent finds each tool of the history in its `Toolset` by name. If the agent does not know a tool, the next call removes it, so the model never sees a tool that is gone. The search only adds, so this is the one source of a removal.
- A deferred tool is a tool of the one `Toolset` with a flag on its info. The agent then has one list of tools, with one rule for names and order. The reasons for the flag are in [Tool](../tool/tool.md).
- The search is a tool of the agent, because it changes the tools that the agent declares. It is the one loader, and the agent has no public method that adds or removes a tool.
- If the model takes a form for a change, the provider sends the change in place. Otherwise, it sends the current tools as the list. A replacement, and an addition on Chat Completions, change the list, and the cache misses on that call.
- The description of the search names each deferred tool. The model then knows the tools that it can load, and loads one by its name. `Toolset` is fixed at `New`, so the description never changes, and a provider caches it.
- The search matches a query in the process, by name or by keyword, with no index. It then works with every provider, and the history keeps the tools that it loads.
- The loaded tools outlive the run, because a failure or a cancel can end a run after the search and before the next model call. The next run then declares the tools, so the model can call a tool that the search told it about.

## Map

| Task                                    | File                                        |
| --------------------------------------- | ------------------------------------------- |
| Change the blocks                       | `pkg/ai/content.go`                         |
| Change the base                         | `pkg/agent/system.go`                       |
| Change the fold of the tools            | `internal/transcript/transcript.go`         |
| Change when the agent declares a change | `pkg/agent/tools.go`, `pkg/agent/system.go` |
| Change how a provider sends a change    | `pkg/ai/<provider>/convert.go`, `params.go` |
| Change the search or the ranking        | `pkg/agent/search.go`                       |
| Change the deferred flag                | `pkg/tool/tool.go`, `pkg/ai/tool.go`        |

## Related Concepts

- [Loop](loop.md)
- [Message](../ai/message.md)
- [State](state.md)
