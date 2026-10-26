---
title: Agent Tool
description: How the agent tool of the harness runs a subagent in a child session, what the parent model gets back, and how a child resumes.
package: pkg/harness, pkg/def, pkg/store/fs
read_when:
  - You give an agent subagents from agent files or from code
  - A subagent runs on a model or with tools that you do not expect
  - A subagent does not resume
---

# Agent Tool

The agent tool gives a task to a named [subagent](../agent/subagent.md). The `agents` section of the system prompt lists the name and the description of each subagent. The model calls the agent tool with a name, a title, and a prompt. The harness runs a child agent to its end and returns its last text.

```
  parent run (session s1)
    model --> agent {"agent":"reviewer","prompt":"..."}
       |
       v
  child agent (session s1.reviewer-a3f9c1, Parent = s1 + call ID)
    its prompt, its tools, the stricter mode
    runs to the end --- start, each event, end --> parent stream
       |
    tool result <-- last text, agent_id, usage
    model --> agent {..., "resume":"s1.reviewer-a3f9c1"}
```

## Design

- A child is an assembly of the same harness with the subagent on top. It then reads the same readers and runs with the same catalog, store, and seeders, and the harness has one path to build an agent.
- A child keeps its log in its own session of the store, as each of Claude Code, [Codex](https://github.com/openai/codex/blob/8270a7c74d73ace00d93cdc5939a06c6cb811b1c/codex-rs/core/src/agent/control.rs), and [oh-my-pi](https://github.com/can1357/oh-my-pi/blob/06aecdd51f07e689e970ceaa180abe2be0c14bbb/packages/coding-agent/src/task/executor.ts) keeps a transcript for each child. The history of the parent then holds only the result, and a UI can open the child.
- The ID of a child is the parent ID, ".", the name, "-", and random hex, as in oh-my-pi. A store that takes no "/" in an ID then keeps it, and the ID shows its parent.
- The result is the last text, the `agent_id`, and the usage, as in [Claude Code](https://code.claude.com/docs/en/sub-agents). The model then knows what the task cost and how to continue it.
- The `resume` input opens the child session again. It takes only a child of the current session, so the model cannot open another session of the store. A subagent of an unsaved agent is unsaved and cannot resume.
- A child gets no agent tool, so the depth is one, as in Claude Code and Codex by default. A task tree then cannot grow without a bound.
- The tool runs the child to its end before it returns. It has no background mode, so the end of the child always comes before the end of the call.
- A child runs in the stricter mode of the parent and its file, with the rules of the parent. A subagent file then cannot widen what a person approved.
- A model of `inherit`, no model, or a model that the catalog does not know, such as the alias `opus`, gives the spec of the parent. A file from Claude Code then runs, and a typo does not stop the task. A model that two providers serve fails the task, so the child does not run on a model that its file did not name.
- The `tools` key takes the tool names of Claude Code as aliases. A missing key gives the child every tool of the parent, and an empty key gives it none.

## Map

| Task                                                | File                       |
| --------------------------------------------------- | -------------------------- |
| Change what the agent tool takes or returns          | `pkg/harness/agent_tool.go` |
| Change the tools, the model, or the mode of a child | `pkg/harness/agent_tool.go` |
| Change what an agent file must hold                 | `pkg/store/fs/agents.go`   |
| Change the fields of a subagent                     | `pkg/def/agent.go`         |
| Change how the prompt lists the subagents           | `pkg/harness/prompt.go`    |

## Related Concepts

- [Subagent](../agent/subagent.md)
- [Harness](harness.md)
- [Definition](../def/definition.md)
- [Permission](permission.md)
- [Value](../session/value.md)
