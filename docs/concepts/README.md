---
title: Concepts
description: One page for each abstraction in gopi.
---

# Concepts

One page for each abstraction in gopi. A page holds the decisions behind the abstraction and a map to its code. The pages are grouped by layer. The rules for a page are in `.claude/rules/concepts.md`.

## ai

| Concept                      | Description                                                                  |
| ---------------------------- | ---------------------------------------------------------------------------- |
| [Message](ai/message.md)     | How one turn of a conversation is typed, stored, and sent to a model.        |
| [Stream](ai/stream.md)       | How one model call reports its events and its final message.                 |
| [Provider](ai/provider.md)   | How a provider serves a model call, and what each capability of a provider asks. |

## tool

| Concept                   | Description                                                                  |
| ------------------------- | ---------------------------------------------------------------------------- |
| [Tool](tool/tool.md)      | How a tool is defined, deferred, run, and wrapped by middleware.             |
