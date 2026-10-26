---
title: Docs
description: The layout of `docs/`, where the rules are, and the glossary.
---

# Docs

The code is the source of truth. `docs/` holds what the code cannot say: the design rules of each part and their reasons, the terms, and a map to the files. If a page and the code disagree, the code is correct and the page has a bug.

These pages are for the developers of gopi. The site shows `concepts/` under Concepts.

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
| host provider | A provider for one server that copies an OpenAI API. It sends its calls through the provider of that API, and lists the models of the server | pseudo provider, gateway, wrapper |
| message       | What one role adds to a conversation, with its content       | turn, entry                 |
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
| CLI agent     | An agent that runs an agent CLI, such as Codex or Claude Code, and reports each run with the events and the log of an agent | wrapper, adapter |
| catalog       | What maps a spec to a model bound to its provider, or to the driver of its agent | registry, model list |
| model file    | One models.json: the default models of each provider and each driver. A program reads it from a path or a URL | model list, manifest, registry |
| spec          | The name of a model in a catalog: `<provider>/<model-id>`, or a bare model ID | model string, model name |
| detector      | What reads one source and gives a catalog a provider or a driver | probe, loader            |
| harness       | What assembles a coding agent from options, the definitions of its readers, and a catalog, on each call | builder, factory, runtime |
| assembly      | What one call of a harness resolved: the model, the instruction files, the skills, the subagents, and the tools | build, config |
| definition    | An agent, a skill, or an instruction text that a person writes, and that a store reads for the harness | spec, manifest, config |
| extra key     | A frontmatter key that no field of a definition takes. The definition keeps it with its value | meta, unknown field |
| instruction file | A file of rules for the agent, such as AGENTS.md or CLAUDE.md | memory file, rules file  |
| skill         | A named body of instructions that the model loads on demand with the skill tool | prompt, recipe, command |
| qualified name | The qualifier of a skill, ":", and its name, such as `ops:deploy` | full name, path name |
| subagent      | A named agent that the model gives a task to with the task tool | worker, delegate, sub-task |
| pinned agent  | The agent file that a session started with, which the host keeps in a session value | saved agent, agent snapshot |
| child session | The session of one subagent run, with its parent in a session value | sidechain, sub-session |
| permission mode | What decides a tool call that no rule matches: default, acceptEdits, plan, or bypassPermissions | approval policy, level |
| permission rule | A tool name, with an optional pattern, that allows, asks for, or denies the calls it matches | policy entry, filter |
| run           | One call of the loop, from a prompt to a result              | invocation, request, turn   |
| turn          | One model call of a run, with the tool calls that it makes   | step, iteration, round      |
| state         | What an agent knows: the messages that the model reads, the progress of the run, the queues, and the loaded tools | context, memory |
| history       | The messages that the model reads before each call: the messages of the log, and the ephemeral messages of the run | transcript, conversation |
| session       | One conversation that a store keeps, with an ID that the application chooses | thread, chat            |
| record        | The part of a session that says that it exists: its ID and its creation time | header, metadata  |
| log           | The live handle on one session: its record, its entries in order, and its values | journal, history file |
| entry         | One item of a log: a message, or a custom entry              | record, row, event          |
| ephemeral entry | An entry that the model reads in its run, and that the store does not keep | transient message, temp entry |
| custom entry  | An entry with a kind and data and no message. The model never reads it | marker, meta entry |
| value         | One piece of application state in a session, named by a namespace and a key | metadata, attribute, setting |
| store         | What keeps the sessions, with the record, the entries, and the values of each. A store can also read the definitions | database, backend, repository |
| snapshot      | A copy of a state, as JSON, that a viewer applies events to. `session.Snapshot` is a session as a store loads it | dump, checkpoint |
| hook          | A function that the loop calls at a lifecycle point          | callback                    |
| hook point    | One point of the loop at which a hook runs, such as BeforeTool. Each point is its own interface | hook type, lifecycle event |
| approve function | The function that decides a tool call that needs a person, the same type for each agent | permission callback, can-use-tool function |
| host          | What runs sessions of any gopi agent for a Go program, with one stream of the state of each session | runtime, orchestrator, platform |
| driver        | What starts an agent of one kind for one session: the harness, the Claude Code agent, or the Codex agent | adapter, backend |
| runner        | The part of an agent that a caller of a driver uses: Run, Log, and State | instance, handle, connection |
| steer         | An entry that the agent reads before its next model call     | interrupt, injection        |
| follow-up     | An entry that the agent reads when the run would stop        | next prompt, queued prompt  |
| middleware    | A function that wraps a tool and returns a tool              | wrapper, decorator, hook    |
| sandbox       | The place where the tools read files, write files, and run commands | box, environment, workspace |
| fence         | An OS sandbox that limits the writes, reads, network, and environment of a command | jail, cage |
| system reminder | The text of a system message, in a user message, for a model that takes no system messages | injected note, hint |
| effort        | How much a model reasons, in the words of its provider API. A thinking level maps to one | reasoning budget, intensity |
| effort message | A system message with no content that sets the effort of the next assistant turn | effort update, level message |
| cache retention | How long a provider keeps the cached prefix of a call: none, short, or long | cache TTL, cache mode |
| placeholder tool | A deferred tool that a request with tool changes declares, so the first change keeps the cache | dummy tool, stub tool |
| MCP server    | A process or an HTTP endpoint that offers tools over the Model Context Protocol | MCP plugin, tool server |
| MCP session   | One connection of gopi to one MCP server                     | MCP client, connection      |
