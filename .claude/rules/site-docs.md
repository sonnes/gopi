---
paths:
  - "web/src/content/docs/**"
---

# Site Pages

These rules apply to every page under `web/src/content/docs/`. The pages are for the users of gopi. The pages under `docs/` are for its developers, and `.claude/rules/docs.md` governs them.

## Who Owns What

| Question                                   | Source                          |
| ------------------------------------------ | ------------------------------- |
| What is the signature, the field, the flag | GoDoc                           |
| What is this thing, and how do I use it    | `ai/`, `tool/`, and `agent/`, one page per abstraction |
| What does one provider add to a call       | `providers/`, one page per provider |
| How do I do one job                        | `guides/*.mdx`, one page per job  |
| Why is it built this way, which file to open | `docs/concepts/`, on GitHub   |
| One name for one thing                     | the glossary in `docs/README.md` |

Do not write a type list, a method list, or a field table. Link GoDoc. Do not write a design rule or its reason. Link the concept page. Do not compare gopi with another project.

## Frontmatter

```yaml
---
title: Streams
summary: One sentence that the index and the lede show.
---
```

`content.config.ts` reads `summary`. `PageTitle.astro` shows it as the lede. Use title case for `title`.

The first paragraph comes after the lede. Do not repeat the summary in it.

## An Abstraction Page

1. In `summary`, say what the abstraction is, in the terms of the glossary.
2. Draw it with a figure. [Figures](#figures) says how to pick the form.
3. If the abstraction is opt-in, start with the case that needs it. Say what the caller gets without it.
4. Show how to use it. Keep a fragment at 15 lines or fewer.
5. Say what a caller can depend on, in one sentence. Do not give the reason.
6. Link the pages that it depends on.
7. End with one line: "The design is in [docs/concepts/ai/x.md](../../../../../docs/concepts/ai/x.md)."

## A Guide

A guide covers one job, not one call. There are six: call a model, stream a
response, use tools, write a provider, run an agent, keep a session. Add a section to one of them before
you add a page.

1. Open with the smallest call that does the job.
2. Give each choice its own section, with a snippet of 15 lines or fewer. A snippet shows one possibility: an option, a branch, a middleware.
3. Use a table for a list of values. Do not draw a figure in a guide. The shape belongs on the abstraction page.
4. End with "## The Full Example". Import one program from `examples/<guide>/main.go` with a `?raw` import, and render it with the Starlight `Code` component inside `<div class="program">`. Set `title` to the path of the program.
5. Say what to read in the output.
6. Link the abstraction pages that the program uses.

A guide has one full example. Every snippet above it is a copy of lines from a
program in `examples/`, or a call that GoDoc shows.

A program in `examples/` runs without a key when it can. Start each guide with a `:::note` that says if its program needs a key. Say it in the doc comment of the program too.

## Examples

- Every complete program lives in `examples/<guide>/`, named for the guide that ends with it. `make check` vets it and `make test` runs its tests.
- A fragment on a page is a copy of lines from a program in `examples/`, or a call that GoDoc shows. If a fragment cannot come from either, write the program.
- Do not paste a program into a page. Import it.

## Figures

A figure is the picture of a page. Pick its form from the question that the
page answers. Each form is a component
in `web/src/components/figures/`. The header comment of a component says what
it shows and when to use it. `web/design/diagrams/` holds the mockup of each
page, with the forms that were not chosen.

| The page answers   | Forms                                                                                                             |
| ------------------ | ----------------------------------------------------------------------------------------------------------------- |
| What is inside it? | `LayerStack`, `AnnotatedCode`, `TypeChain`, `SignatureAnatomy`, `BindingEquation`, `Nesting`, `Onion`, `ToolFaces`, `SpecSheet`, `UsageBar`, `PrefixBar` |
| In what order?     | `Lanes`, `Spans`, `OptionTape`, `Transcript`, `TraceTable`, `LoopCycle`, `Ledger`, `Rails`, `FileView`, `ReplayTable`, `HookRail` |
| Which one applies? | `CheckMatrix`, `Ladder`, `StopFork`, `FailureFork`, `OpenFork`, `Hub`, `ConstructorCards`, `ValuePair`, `StopOrCancel` |

To add a figure:

1. Find the question that the page answers.
2. If a form in the table answers that question, use its component.
3. If no form fits, draw two or three forms as a mockup in `web/design/diagrams/parts/`. Its README says how.
4. Build the chosen form as a new component in `web/src/components/figures/`. Add it to the table above.

- Wrap the drawing in `Figure`. Give every figure an `alt` that says what the figure shows.
- Put the content in props and the layout in the component. Then the next feature can use the same form.
- Use only tokens in the scoped style of a component, so the figure follows the theme.
- Fit a figure in the reading column, 552px inside the panel. A wider figure scrolls.
- A figure replaces the text and the table that say the same thing. Do not write both.
- Keep a list of values as a table, unless a figure shows a pattern in it, such as a check matrix or a ladder.
- An MDX prop loses the leading spaces of a string with more than one line. Put indented code in an `export const` under the imports.
- `docs/` keeps ASCII diagrams. GitHub renders those, and it does not run the site build.

## Links

- Write a link as a relative path to the file: `../ai/streams.mdx`, or `../../../../../docs/concepts/ai/stream.md`. The build turns a page path into a route and any other path into a GitHub link.
- `make docs-links` fails on a dead link and on a site-absolute link.
- Do not put a link in a component prop. The link checker and the rewriter read Markdown links only.

## Writing

- Keep a page at 600 words of prose or fewer. A program and a figure do not count.
- Write a procedure as a numbered list in the imperative. Put the condition before the command.
- Use one name for one thing. The glossary in `docs/README.md` has the names.
- Use title case for headings. Write an article, and a preposition or conjunction of three letters or fewer, in lowercase. The first word is always capitalized.
