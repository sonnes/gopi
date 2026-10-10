---
title: Section
description: How a named part of the system prompt changes from a place in a conversation, and how the agent keeps the history in step with it.
package: pkg/ai, pkg/agent
read_when:
  - A part of the system prompt changes during a conversation
  - A provider sends system text that you do not expect
  - A saved history has a prompt that you do not expect
---

# Section

A section is a named part of the system prompt. A system message sets a section or removes it, from its place in the conversation. The `System` option of the agent builds the whole prompt as a list of sections. A prompt that never changes is one section.

```
  agent.System(fn)
     |
  before each model call: fn() != sections of the history?
  (after turn_start, before the new messages)
     | yes
     v
  order kept?  -- yes -->  patch: set changed and new, remove missing
     | no
     v
  reset: remove every section, set all again
     |
  one system message --> history
    SectionRemoved..., Section..., ToolRemoved..., ToolAdded...
     |
  first message of the history, the base  -->  ai.SystemPrompt, ai.Tools
  each later system message               -->  in place, framed by name
```

## Design

- A section is a content block of a system message, as in [Message](../ai/message.md). The content of a message is an ordered list, so the history keeps the order of the sections.
- A removal is its own block, `SectionRemoved`, because a section with an empty text is a valid section.
- The agent has one prompt option, `System`, and its function returns the sections. The whole prompt then lives in the history, and a saved history records the prompt that the model read.
- The agent calls the function one time for each model call. The change goes before the new messages of the turn, so the model reads the prompt before the messages that it applies to.
- A provider puts the base in the system prompt and the tool list of its API, with no frame. The base never changes, so the cached prefix stays the same across a change, and a change costs only the tokens of its system message. The prefix changes on a call where a provider sends a new tool list, as [Tool Change](tool-change.md) shows.
- The agent folds the sections, and a provider does not, because only the agent compares the history with the sections that it wants. A provider adds only the frame of [Message](../ai/message.md), so a tag or a heading in the text is the choice of the caller.
- The section changes and the tool changes go in one system message, so a turn adds at most one system message.
- If it can, a change keeps the order of the history, and if it cannot, it resets every section. A reset is longer, but it gives the order of `fn`, so the model reads one order.
- An agent without `System` leaves the sections of the history as they are. A caller that does not use sections then removes nothing that another agent wrote.
- A provider sends each system message in its place, and never merges the sections into one prompt. The reasons are in [Message](../ai/message.md).

## Map

| Task                                              | File                           |
| ------------------------------------------------- | ------------------------------ |
| Change the section blocks                         | `pkg/ai/content.go`            |
| Change the fold or when the agent writes a change | `pkg/agent/system.go`          |
| Change how a provider sends a system message      | `pkg/ai/<provider>/convert.go` |

## Related Concepts

- [Tool Change](tool-change.md)
- [Message](../ai/message.md)
- [Loop](loop.md)
