---
title: Concepts
description: One page for each abstraction in gopi.
---

# Concepts

One page for each abstraction in gopi. A page holds the design rules of the abstraction and a map to its code. The pages are grouped by layer. The rules for a page are in `.claude/rules/concepts.md`.

## ai

| Concept                      | Description                                                                  |
| ---------------------------- | ---------------------------------------------------------------------------- |
| [Message](ai/message.md)     | How one turn of a conversation is typed, stored, and sent to a model.        |
| [Stream](ai/stream.md)       | How one model call reports its events and its final message.                 |
| [Provider](ai/provider.md)   | How a provider serves a model call, and how the Anthropic provider builds its requests. |
| [OpenAI Providers](ai/openai.md) | How the two OpenAI providers build their requests, keep reasoning between calls, and serve compatible servers. |

## tool

| Concept                   | Description                                                                  |
| ------------------------- | ---------------------------------------------------------------------------- |
| [Tool](tool/tool.md)      | How a tool is defined, deferred, run, and wrapped by middleware.             |

## agent

| Concept                             | Description                                                                                   |
| ----------------------------------- | --------------------------------------------------------------------------------------------- |
| [Loop](agent/loop.md)               | How an agent runs a model with tools until the work is done, and where its history lives.     |
| [Run Stream](agent/run-stream.md)   | How one run reports its events in order, and how a caller saves the messages of a run.        |
| [State](agent/state.md)             | How an agent keeps what it knows in one value that only events change, so a viewer and a hook read the same state as the loop. |
| [Section](agent/section.md)         | How a named part of the system prompt changes from a place in a conversation, and how the agent keeps the history in step with it. |
| [Tool Change](agent/tool-change.md) | How an agent loads a deferred tool on demand, and how it adds and removes the tools that the model can call in a conversation. |

## sandbox

| Concept                        | Description                                                                                                      |
| ------------------------------ | ---------------------------------------------------------------------------------------------------------------- |
| [Sandbox](sandbox/sandbox.md)  | How the file and shell tools reach a directory on this host or a remote machine through one small interface.   |
| [Fence](sandbox/fence.md)      | How a command runs inside an OS sandbox that limits its writes, its reads, its network, and its environment.   |
