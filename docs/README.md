---
title: Docs
description: The layout of `docs/`, where the rules are, and the glossary.
---

# Docs

The code is the source of truth. `docs/` holds what the code cannot say: the decisions behind each part, the terms, and a map to the files. If a page and the code disagree, the code is correct and the page has a bug.

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
| section       | A named part of the system prompt. The SystemPrompt option sets it. A system message sets or removes it from its place. | prompt part, fragment, slot |
| tool change   | A system message block that adds or removes a tool from its place | tool update, tool delta  |
| stream        | The events of one run, read in order                         | channel, feed, subscription |
| event         | One thing that happened inside a run                         | message, update, signal     |
| option        | One typed setting of a model call                            | parameter, flag, config     |
| cost          | What one model response cost, in US dollars                  | price, spend, charge        |
