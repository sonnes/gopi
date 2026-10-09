---
title: Docs
description: The layout of `docs/`, where the rules are, and the glossary.
---

# Docs

The code is the source of truth. `docs/` holds what the code cannot say: the design rules of each part and their reasons, the terms, and a map to the files. If a page and the code disagree, the code is correct and the page has a bug.

These pages are for the developers of gopi.

## Layout

| Directory   | Contents                                                                  | Name                   | Rules                                                     |
| ----------- | ------------------------------------------------------------------------- | ---------------------- | --------------------------------------------------------- |
| `concepts/` | One page per concept, grouped by layer. `concepts/README.md` is the index. | `<layer>/<concept>.md` | [.claude/rules/concepts.md](../.claude/rules/concepts.md) |

The writing rules for every file here are in [.claude/rules/docs.md](../.claude/rules/docs.md). Claude Code loads each rules file when it works on a matching file.

## Glossary

Add a term when you name a new abstraction. Keep one name for one thing.

| Term          | Meaning                                                     | Do not write                |
| ------------- | ----------------------------------------------------------- | --------------------------- |
| provider      | One backend that answers a model call                        | vendor, service, client     |
| model         | One named model that a provider serves                       | engine, LLM                 |
| message       | One turn of a conversation, with a role and content          | turn, entry                 |
| content block | One part of a message: text, thinking, a tool call, or a file | part, chunk, segment        |
| tool          | One function that a model can call                           | function, action, skill     |
| base          | The first message of a history, if it is a system message. It holds the first sections and the first tools | initial prompt, header |
| section       | A named part of the system prompt. The SystemPrompt option sets it. A system message sets or removes it from its place. | prompt part, fragment, slot |
| tool change   | A system message block that adds or removes a tool from its place | tool update, tool delta  |
| stream        | The events of one run, read in order                         | channel, feed, subscription |
| event         | One thing that happened inside a run                         | message, update, signal     |
| option        | One typed setting of a model call                            | parameter, flag, config     |
| cost          | What one model response cost, in US dollars                  | price, spend, charge        |
| deferred tool | A tool that the agent can run but does not send until a change adds it | lazy tool, hidden tool |
| sequential tool | A tool whose call runs with no other call of its message at the same time | exclusive tool, serial tool |
| search tool   | The agent tool that ranks the deferred tools and loads the matches | tool finder, loader   |
| agent         | The loop that runs a model with tools until the work is done | assistant, bot              |
| run           | One call of the loop, from a prompt to a result              | invocation, request, turn   |
| state         | What an agent knows: the messages that the model reads, the progress of the run, the queues, and the loaded tools | context, memory |
| history       | The messages of an agent, in order, in memory. The model reads them before each call | log, transcript, session |
| snapshot      | A copy of a state, as JSON, that a viewer applies events to   | dump, checkpoint            |
| hook          | A function that the loop calls at a lifecycle point          | callback                    |
| steer         | A message that the agent reads before its next model call    | interrupt, injection        |
| follow-up     | A message that the agent reads when the run would stop       | next prompt, queued prompt  |
| middleware    | A function that wraps a tool and returns a tool              | wrapper, decorator, hook    |
| system reminder | The text of a system message, in a user message, for a model that takes no system messages | injected note, hint |
| effort        | How much a model reasons, in the words of its provider API. A thinking level maps to one | reasoning budget, intensity |
| effort message | A system message with no content that sets the effort of the next assistant turn | effort update, level message |
| cache retention | How long a provider keeps the cached prefix of a call: none, short, or long | cache TTL, cache mode |
| placeholder tool | A deferred tool that a request with tool changes declares, so the first change keeps the cache | dummy tool, stub tool |
