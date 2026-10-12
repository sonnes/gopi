---
title: Concepts
description: One page for each abstraction in gopi.
---

# Concepts

One page for each abstraction in gopi. A page holds the design rules of the abstraction and a map to its code. The pages are grouped by layer. The rules for a page are in `.claude/rules/concepts.md`.

## ai

| Concept                      | Description                                                                  |
| ---------------------------- | ---------------------------------------------------------------------------- |
| [Message](ai/message.md)     | How one message of a conversation is typed, stored, and sent to a model.     |
| [Stream](ai/stream.md)       | How one model call reports its events and its final message.                 |
| [Provider](ai/provider.md)   | How a provider serves a model call, and how the Anthropic provider builds its requests. |
| [OpenAI Providers](ai/openai.md) | How the two OpenAI providers build their requests, keep reasoning between calls, and serve compatible servers. |

## tool

| Concept                   | Description                                                                  |
| ------------------------- | ---------------------------------------------------------------------------- |
| [Tool](tool/tool.md)      | How a tool is defined, deferred, run, and wrapped by middleware.             |
| [MCP](tool/mcp.md)        | How the tools of an MCP server become tools of an agent, and how the CLI starts the servers of an mcp.json file. |

## agent

| Concept                             | Description                                                                                   |
| ----------------------------------- | --------------------------------------------------------------------------------------------- |
| [Loop](agent/loop.md)               | How an agent runs a model with tools until the work is done, and where a hook changes a run. |
| [Durability](agent/durability.md)   | How a run stores each message before its event, what a crash loses, and how an agent resumes a session. |
| [Run Stream](agent/run-stream.md)   | How one run reports its events in order, and what a message_end promises about the log.       |
| [State](agent/state.md)             | How an agent keeps what it knows in one value that only events change, so a viewer and a hook read the same state as the loop. |
| [Section](agent/section.md)         | How a named part of the system prompt changes from a place in a conversation, and how the agent keeps the history in step with it. |
| [Tool Change](agent/tool-change.md) | How an agent loads a deferred tool on demand, and how it adds and removes the tools that the model can call in a conversation. |

## session

| Concept                    | Description                                                                                   |
| -------------------------- | --------------------------------------------------------------------------------------------- |
| [Log](session/log.md)      | How a session keeps its history as an append-only list of entries that the model reads in order. |
| [Store](session/store.md)  | How a session record, its entry log, and its values are kept, and what a store must promise.  |
| [Value](session/value.md)  | How a session keeps application state next to its entries, and why the model never reads it.  |

## sandbox

| Concept                        | Description                                                                                                      |
| ------------------------------ | ---------------------------------------------------------------------------------------------------------------- |
| [Sandbox](sandbox/sandbox.md)  | How the file and shell tools reach a directory on this host or a remote machine through one small interface.   |
| [Fence](sandbox/fence.md)      | How a command runs inside an OS sandbox that limits its writes, its reads, its network, and its environment.   |
