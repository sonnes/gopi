---
title: Subagent
description: How the task tool runs a subagent in a child session, what the parent model gets back, and how a child resumes.
package: pkg/harness, pkg/harness/subagent, pkg/harness/files
read_when:
  - You give an agent subagents from agent files or from code
  - You show the work of a subagent in a UI
  - A subagent runs on a model or with tools that you do not expect
---

# Subagent

A subagent is a named agent that the model gives a task to. The `agents` section of the system prompt lists the name and the description of each subagent. The model calls the task tool with a name and a prompt. The harness runs a child agent to its end and returns its last text.

```
  parent run (session s1)
    model --> task {"agent":"reviewer","prompt":"..."}
       |
       v
  child agent (session s1.reviewer-a3f9c1, Parent = s1 + call ID)
    its prompt, its tools, the stricter mode
    runs to the end --- each event --> SubagentEvents(call ID, e)
       |
    tool result <-- last text, agent_id, usage
    model --> task {..., "resume":"s1.reviewer-a3f9c1"}
```

## Design

- A child is an assembly of the same harness with the subagent on top. It then reads the same sources and runs with the same catalog, store, and seeders, and the harness has one path to build an agent.
- A child keeps its log in its own session of the store, as each of Claude Code, [Codex](https://github.com/openai/codex/blob/8270a7c74d73ace00d93cdc5939a06c6cb811b1c/codex-rs/core/src/agent/control.rs), and [oh-my-pi](https://github.com/can1357/oh-my-pi/blob/06aecdd51f07e689e970ceaa180abe2be0c14bbb/packages/coding-agent/src/task/executor.ts) keeps a transcript for each child. The history of the parent then holds only the result, and a UI can open the child.
- The ID of a child is the parent ID, ".", the name, "-", and random hex, as in oh-my-pi. A store that takes no "/" in an ID then keeps it, and the ID shows its parent.
- A child names its parent and the task call in the `Parent` value, as a Codex thread keeps `parent_thread_id`. The link is data, so a store needs no folder layout to keep it.
- The result is the last text, the `agent_id`, and the usage, as in [Claude Code](https://code.claude.com/docs/en/sub-agents). The model then knows what the task cost and how to continue it.
- The `resume` input opens the child session again. It takes only a child of the current session, so the model cannot open another session of the store. A subagent of an unsaved agent is unsaved and cannot resume.
- A child gets no task tool, so the depth is one, as in Claude Code and Codex by default. A task tree then cannot grow without a bound.
- The events of a child go to `SubagentEvents` and not to the stream of the parent. The run stream keeps its order rules, and the parent stream shows the task as one tool call.
- A child runs in the stricter mode of the parent and its file, with the rules of the parent. A subagent file then cannot widen what a person approved.
- A model of `inherit`, no model, or a model that the catalog cannot resolve, such as the alias `opus`, gives the spec of the parent. A file from Claude Code then runs, and a typo does not stop the task.
- The `tools` key takes the tool names of Claude Code as aliases. A missing key gives the child every tool of the parent, and an empty key gives it none.

## Map

| Task                                               | File                            |
| -------------------------------------------------- | ------------------------------- |
| Change what the task tool takes or returns         | `pkg/harness/task_tool.go`      |
| Change the tools, the model, or the mode of a child | `pkg/harness/task_tool.go`     |
| Change what an agent file must hold                | `pkg/harness/files/agents.go`   |
| Change the fields of a subagent                    | `pkg/harness/subagent/agent.go` |
| Change how the prompt lists the subagents          | `pkg/harness/prompt.go`         |

## Related Concepts

- [Harness](harness.md)
- [Permission](permission.md)
- [Run Stream](../agent/run-stream.md)
- [Value](../session/value.md)
