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
| [Host Provider](ai/host-provider.md) | How a provider for a server that copies an OpenAI API sends its calls through the provider of that API, and lists the models of the server with their limits and prices. |

## catalog

| Concept                          | Description                                                                                              |
| -------------------------------- | -------------------------------------------------------------------------------------------------------- |
| [Catalog](catalog/catalog.md)    | How a spec resolves to a model bound to its provider or to the driver of its agent, and how detectors fill a catalog from the credentials of a process. |
| [Model Data](catalog/model-data.md) | How make models writes the default models of each provider and driver to models.json, how a program reads the file, and how a fetch updates a catalog at runtime. |

## tool

| Concept                   | Description                                                                  |
| ------------------------- | ---------------------------------------------------------------------------- |
| [Tool](tool/tool.md)      | How a tool is defined, deferred, run, and wrapped by middleware.             |
| [MCP](tool/mcp.md)        | How an agent gets the tools of an MCP server. A CLI agent connects to its own servers, and the harness connects with pkg/mcp. |

## agent

| Concept                             | Description                                                                                   |
| ----------------------------------- | --------------------------------------------------------------------------------------------- |
| [Loop](agent/loop.md)               | How an agent runs a model with tools until the work is done, and where a hook changes a run. |
| [Durability](agent/durability.md)   | How a run stores each message before its event, what a crash loses, and how an agent resumes a session. |
| [Run Stream](agent/run-stream.md)   | How one run reports its events in order, and what a message_end promises about the log.       |
| [State](agent/state.md)             | How an agent keeps what it knows in one value that only events change, so a viewer and a hook read the same state as the loop. |
| [Section](agent/section.md)         | How a named part of the system prompt changes from a place in a conversation, and how the agent keeps the history in step with it. |
| [Tool Change](agent/tool-change.md) | How an agent loads a deferred tool on demand, and how it adds and removes the tools that the model can call in a conversation. |
| [CLI Agent](agent/cli-agent.md)     | How an agent CLI such as Codex or Claude Code runs as a gopi agent, with the same events, state, and log as the loop. |
| [Driver](agent/driver.md)           | How one interface starts the harness, Claude Code, or Codex for a session, and what a driver does with a field or a hook point that its agent cannot honor. |

## def

| Concept                          | Description                                                                                                  |
| -------------------------------- | ------------------------------------------------------------------------------------------------------------ |
| [Definition](def/definition.md)  | How an agent, a skill, and an instruction text reach an agent from any store through one reader interface for each. |

## harness

| Concept                              | Description                                                                                                                     |
| ------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------- |
| [Harness](harness/harness.md)        | How a harness assembles a coding agent from options, the definitions of its readers, and a catalog, on each call.     |
| [Skill](harness/skill.md)            | How a skill reaches the model on demand through the skill tool, how the system prompt lists the skills, and how two skills with one name both stay. |
| [Subagent](harness/subagent.md)      | How the task tool runs a subagent in a child session, what the parent model gets back, and how a child resumes.                  |
| [Permission](harness/permission.md)  | How a permission mode and the allow, ask, and deny rules decide if a tool call runs, asks a person, or is denied.                 |

## host

| Concept              | Description                                                                                                                        |
| -------------------- | ---------------------------------------------------------------------------------------------------------------------------------- |
| [Host](host/host.md) | How a host keeps the sessions of any gopi agent for a Go program, as resources with an ID, a config, and a status that survive a restart. |

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
