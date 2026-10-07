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
