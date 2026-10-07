---
paths:
  - "**/*_test.go"
---

# Go Tests

- Use `github.com/stretchr/testify`. Use `require` for fatal checks and `assert` for non-fatal checks.
- If a function has several input cases, write a table-driven test.
- Keep the tests simple. The tests and their comments describe the current state of the code, not its history.
- Test behavior that a caller can see, not implementation details. Do not write a test only to increase coverage.
- If a test calls an external API, record and replay the traffic. Keep the recordings in the repository.
