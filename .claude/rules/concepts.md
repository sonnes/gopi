---
paths:
  - "docs/concepts/**"
---

# Concept Pages

A concept is one abstraction that a reader must understand to change gopi. A provider, a run stream, and a state are concepts. A helper function is not. Each concept has one page in `docs/concepts/<layer>/`, where the layer is the directory under `pkg/` that implements it, such as `ai`.

A page holds the design rules of the concept and a map to the code. The code holds everything else. Read `.claude/rules/docs.md` first.

## When To Write

- If you add an abstraction, add a page in the same change.
- If you change a design rule, update the page in the same change.
- If a change touches no design rule and no term, do not touch the page. Update the doc comment in the code.
- If you remove an abstraction, remove the page.
- After each change, update the index in `docs/concepts/README.md`.

## Frontmatter

Start each page with this frontmatter:

```yaml
---
title: Run Stream
description: How one run reports its events, its result, and its errors.
package: pkg/agent
read_when:
  - You consume the events of a run
  - A run ends without a result
---
```

- `title` is also the H1.
- `description` is one sentence. The index shows it.
- `package` names the Go packages that implement the concept.
- `read_when` lists the tasks that bring a reader to the page.

## Content

Keep a page at 600 words or fewer. If a page grows past that, review it. Split the concept only when the page holds more than one.

Write what the code cannot say:

- What the concept is, in the terms of the glossary.
- A design rule: what the code does now, and the reason. Write one bullet per rule.
- A guarantee: what a caller can depend on, and the condition that breaks it.
- A rule that spans packages, and its reason.
- A diagram of a shape that is hard to read as a sentence: a flow, a hierarchy, or a sequence.
- The file to open for each task, in a table.

Do not write what the code states: struct fields, method lists, interface methods, flags, environment variables, file layouts, wire examples, and timeout values. Name a type or a function only when the reader must go to it. Do not describe history or plans. Do not write an alternative that the code does not use, and do not compare gopi with another project.

If a reason belongs to one function or one type, write it in the doc comment and not on the page. If a reason spans packages, write it on the page. Then link the page from the package doc: "The reasons are in docs/concepts/agent/run-stream.md."

## Structure

1. Make the H1 the title.
2. In the first paragraph, say what the concept is.
3. If the concept has a shape, draw it. Put the diagram after the first paragraph.
4. Make the next H2 "Design". Write one bullet per rule or guarantee. Start the bullet with what the code does, then give the reason. If the rule gives something up, name the cost. If a guarantee can break, name the condition.
5. Keep each bullet at three sentences or fewer. If a rule needs more, it is two rules, or it belongs in a doc comment.
6. Make the next H2 "Map". Make it a table with two columns: the task, and the file to open.
7. Make the last H2 "Related Concepts". Link to the pages that this page depends on.
