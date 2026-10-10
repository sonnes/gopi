---
paths:
  - "**/*.go"
---

# Go Code

Write vertical, readable code. If you must choose, use more lines instead of longer lines:

- In a long function call, put each argument on its own line. Put a comma after the last argument.
- Use intermediate variables instead of deeply nested expressions.
- Give a long conditional a named boolean.
- Return early to keep the logic flat.
- In a struct literal, put each field on its own line.

Keep packages small and focused. Define an interface in the package that needs it. Implement it in another package.

The code is the source of truth, and a doc never restates it. So the code must say what it does:

- Give each package a doc comment. Say what the package is and the rules that a caller must obey.
- Give each exported name a doc comment. If a choice has a reason, write the reason in the comment.
- Give each number a named constant.
- If a field or an option has a default, say the default in its doc comment. If a condition changes the default, say the condition.
- Link a type in a doc comment as `[pkg.Type]`.

If you add an abstraction, or make or reverse a decision, update `docs/concepts/` in the same change. If you change only a behavior, update the doc comment. Read `.claude/rules/concepts.md` first.
