---
title: MCP
description: How the tools of an MCP server become tools of an agent, and how the CLI starts the servers of an mcp.json file.
package: pkg/mcp, cmd/gopi
read_when:
  - You give an agent the tools of an MCP server
  - You add a server to an mcp.json file for the CLI
  - An MCP tool returns a result that you do not expect
---

# MCP

An MCP server is a process or an HTTP endpoint that offers tools over the Model Context Protocol. `mcp.Tools` lists the tools of one MCP session and returns one tool for each. The agent runs an MCP tool as it runs any other tool.

```
  mcp.json --> cmd/gopi: one MCP session for each server
                  |
                  v
  mcp.Tools(session) --> []tool.Tool --> agent.Toolset
                                             |
                       model calls a tool    v
  tool.Run --> session.CallTool --> MCP server --> ai.ToolResult
```

## Design

- `pkg/mcp` is its own module, as each provider is. The root module then does not depend on the MCP SDK. The cost: a caller adds one more module.
- `mcp.Tools` returns tools from `tool.Func`, so the agent, the providers, and each middleware need no MCP code. Only `pkg/mcp` and the CLI import the MCP SDK.
- The caller owns the MCP session. `mcp.Tools` does not connect or close, so the caller picks the transport and the lifetime. After the caller closes the session, each call of a tool returns a Go error.
- An MCP result with `isError` becomes an error result, and a failed call becomes a Go error. This obeys the two failure channels in [Tool](tool.md).
- Text and images keep their type, and a text resource becomes its text. Each other block, such as audio or a binary resource, becomes its JSON text, because `ai.Content` has no block for it. If a result has only structured content, the model gets its JSON text.
- `mcp.Tools` changes each character that a provider rejects in a tool name to `_`, and it cuts the name to the length limit of the providers. If two tools then have the same name, `mcp.Tools` returns an error, because the model calls a tool by its name.
- The tools are fixed when `mcp.Tools` returns. `agent.Toolset` is fixed at `agent.New` or `agent.Durable`, so a change of the tool list that a server announces does not reach the model.
- The CLI defers each MCP tool, because one server can have many tools. The search tool loads them, as in [Tool Change](../agent/tool-change.md).
- The CLI names each tool `mcp__<server>__<tool>`, so two servers with the same tool name do not collide. If two server names, such as `a.b` and `a_b`, give one tool name, the CLI keeps the first server and warns about the other.
- If a server does not connect, the CLI writes a warning and continues without its tools. One server that is down then does not stop the work. A file that the CLI cannot read stops gopi.
- The CLI connects to all servers at the same time, so servers that do not answer delay the start by one timeout in total.
- If a stdio server does not connect, the warning shows the end of its stderr. The CLI discards the rest of the stderr, so it does not mix with the output of a run.
- The fence does not confine an MCP server. A stdio server is a normal process of the user, so it can write any file and use the network. The CLI starts a server only from the file that `--mcp` names.

## Map

| Task                                      | File                  |
| ----------------------------------------- | --------------------- |
| Change how an MCP tool becomes a tool     | `pkg/mcp/mcp.go`      |
| Change how a result becomes content       | `pkg/mcp/mcp.go`      |
| Change the format of the mcp.json file    | `cmd/gopi/mcp.go`     |
| Add a transport to the CLI                | `cmd/gopi/mcp.go`     |
| Change the flag                           | `cmd/gopi/main.go`    |

## Related Concepts

- [Tool](tool.md)
- [Tool Change](../agent/tool-change.md)
- [Fence](../sandbox/fence.md)
