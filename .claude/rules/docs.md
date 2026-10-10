---
paths:
  - "docs/**/*.md"
---

# Docs

These rules apply to every Markdown file under `docs/`.

## The Code Is The Source Of Truth

No test checks a doc against the code, so a doc drifts. If a doc and the code disagree, a reader cannot tell which one is true. So a doc never restates what the code says.

A doc holds only what the code cannot say:

- A design rule: what the code does, and the reason.
- A term: the glossary in `docs/README.md`.
- A map: the file to open for a task.

Before you write a line, ask if a reader can get it from one file of code. If yes, delete the line. If the code does not say it, write it in a doc comment in the code. If a test can check the line, write the test.

Do not write a type list, a method list, or a flag list. Do not write an environment variable table, a file tree, a wire example, or a timeout value. The code, the GoDoc, and `--help` hold them. Do not write history or plans. Do not write an alternative that the code does not use. Do not compare gopi with another project. To credit a design from another project, obey [Attribution](#attribution).

## Attribution

- If a design rule comes from another project, name that project in the bullet of the rule.
- Link to the source of the design at a fixed commit or tag, so that the link stays correct.
- Write the credit as one fact: "The event order comes from [Project](https://github.com/<owner>/<repo>/blob/<sha>/<path>)."
- Do not compare the two designs. Do not say which design is better.
- If gopi changes the borrowed design, write what gopi does. Do not describe the design of the other project.
- If code, text, or test cases come from another project, write the credit in a doc comment next to them.

## Writing

- Write a procedure as a numbered list in the imperative. Keep each step at 20 words or fewer. Put the condition before the command.
- Use one name for one thing. The glossary in `docs/README.md` has the names. If you need a new term, add it to the glossary.
- Write each fact on one page. Link to it from the other pages.
- Do not write a vague size or rate, such as "fast", "large", or "rare". Name the condition, or link the constant in the code.
- Use title case for headings.
- If a shape is hard to read as a sentence, draw it. A flow, a hierarchy, and a sequence are shapes.
- Draw a diagram in ASCII, in a fenced block. Do not use Mermaid, an image, or a Unicode box character.
- Keep a diagram at 72 columns or fewer, so it fits a terminal and a diff.
- A diagram replaces the text that describes the shape. Do not write both.

## Go Examples

- Write an example only when it shows a design rule that prose cannot show.
- Compile each example that you write. An example that does not compile is a bug.
- If an example belongs to one package, write it as an `Example` test instead. Then the tests check it.
