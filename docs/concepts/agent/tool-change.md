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

A tool change adds tools to the tools that the model can call, or removes tools, from a place in the conversation. The search tool of the agent loads a deferred tool.

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

- A change is a system message with `ToolAdded` and `ToolRemoved` blocks, and the base stays the same, as in [Section](section.md). A deploy that changes a tool then keeps the cache of a saved history. The cache misses on a call where the provider sends a new tool list.
- The history declares the tools that the model can call. The search keeps the loaded tools in the agent and writes nothing to the history, so only the next model call writes a change.
- The agent and each provider fold the tools with `transcript.CurrentTools`, so they agree on the tools at each place. The fold is in `internal/transcript`, because `pkg/ai` holds no conversation state.
- If a tool of the history is not in `Toolset`, the next call removes it, so the model never sees a tool that is gone. The search only adds, so this is the one source of a removal.
- A deferred tool is in the one `Toolset`, with a flag on its info, so the agent has one rule for names and order. The reasons for the flag are in [Tool](../tool/tool.md).
- The search is a tool of the agent, because it changes the tools that the agent declares. It is the one loader: the agent has no public method that adds or removes a tool.
- The description of the search names each deferred tool, so the model loads a tool by its name. The cost: each name costs tokens. `Toolset` is fixed at `New`, so a provider caches the description. An agent with other deferred tools replaces the search tool in a saved history.
- The search matches a query by name or by keyword in the process, with no index, so it works with every provider. The cost: a synonym of a word of a tool does not match.
- The loaded tools outlive the run, because a failure or a cancel can end a run after the search and before the next model call. The next run then declares them, so the model can call a tool that the search told it about.

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
