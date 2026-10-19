---
title: Permission
description: How a permission mode and the allow, ask, and deny rules decide if a tool call runs, asks a person, or is denied.
package: pkg/harness/permission, pkg/harness
read_when:
  - You decide which tool calls of an agent run without a question
  - You ask a person to approve a tool call
  - A tool call runs, asks, or is denied when you do not expect it
---

# Permission

A permission policy decides, for each tool call, if the call runs, asks a person, or is denied. It holds a mode and three lists of rules in the format of the settings of Claude Code: `bash(git push *)`, `edit_file(docs/**)`, `mcp__github__*`. The policy becomes one `BeforeTool` hook of the agent.

```
  tool call
     |
  Deny rule? ---- yes --> denied, the model gets the rule
     |
  Ask rule? ----- yes --> Approve --> run, or denied
     |
  Allow rule? --- yes --> run
     |
  mode x kind     Read    Edit    Exec    Other
    default       run     ask     ask     ask
    acceptEdits   run     run     ask     ask
    plan          run     deny    deny    deny
    bypass        run     run     run     run
```

## Design

- The order of the checks, the rule format, the tool aliases, and the match rules for commands and paths come from [Claude Code](https://code.claude.com/docs/en/permissions). A rule from a Claude Code settings file then works in gopi as it is.
- The policy is one `BeforeTool` hook over `agent.Deny`. The loop stays one model with tools, and the model gets the reason of each denial as a tool result, so it can try another way.
- A Deny rule and an Ask rule apply in every mode, also in bypass. A person can then let an agent run freely and still block one command.
- A tool has a kind, and the mode decides by the kind. A tool that the policy does not know is `Other`, and it asks in every mode but bypass. A new tool then needs an approval or a rule before it runs.
- Without `Approve`, each call that needs an approval is denied, with a reason that says that no one can approve. An agent with no person present then fails closed.
- `AllowAlways` keeps an allow rule for the exact command, or for the tool name, in the hook. A new hook starts with no kept rules, so an approval lasts as long as one agent and does not reach the next session.
- The policy is a plain value, and the state of a run lives in its hook. The harness then copies the policy for each call and each child with no shared lock.
- A command rule matches the text of the command, not a parsed shell program. So a command rule is not a security boundary, and the fence of the sandbox limits what a command that runs can touch.
- An invalid rule fails the assembly. A typo in a Deny rule then cannot let every call through.

## Map

| Task                                         | File                                |
| -------------------------------------------- | ----------------------------------- |
| Change the order of the checks               | `pkg/harness/permission/policy.go`  |
| Change the kind of a built-in tool           | `pkg/harness/permission/kind.go`    |
| Change how a rule is parsed, or a tool alias | `pkg/harness/permission/rule.go`    |
| Change how a command rule matches            | `pkg/harness/permission/shell.go`   |
| Change how a path rule matches               | `pkg/harness/permission/path.go`    |
| Change how the CLI asks a person             | `cmd/gopi/ask.go`                   |

## Related Concepts

- [Harness](harness.md)
- [Loop](../agent/loop.md)
- [Fence](../sandbox/fence.md)
