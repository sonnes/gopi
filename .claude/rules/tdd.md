---
paths:
  - "**/*.go"
---

# Test First

If you add or change behavior, write the test before the implementation:

1. Write a test for the new behavior.
2. Run the test and make sure that it fails for the right reason.
3. Write the minimum code that makes the test pass.
4. Run the test again and make sure that it passes.

The cycle is the review gate for every change, including a small one. A test that is written after the code asserts what the code does, not what it must do.
