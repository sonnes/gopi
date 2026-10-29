---
title: MCP
description: How an agent gets the tools of an MCP server. A CLI agent connects to its own servers, and the harness connects with pkg/mcp.
package: pkg/mcp, pkg/agent, pkg/harness, cmd/gopi
read_when:
  - You give a session or an agent MCP servers
  - You add a server to an mcp.json file for the CLI
  - An MCP tool returns a result that you do not expect
---

# MCP

An MCP server is a process or an HTTP endpoint that offers tools over the Model Context Protocol. A session names its servers in its config, and each driver gives them to its agent. The harness connects with `mcp.Connect`, and the agent runs an MCP tool as it runs any other tool.

```mermaid
flowchart TD
  accTitle: How an agent gets the tools of an MCP server
  accDescr: The Claude Code and Codex drivers give the MCP servers of the config to the CLI, which connects. The harness connects with mcp.Connect, and each tool of a server becomes a tool of the agent.
  config[agent.Config.MCP] --> claude[claude driver: --mcp-config]
  config --> codex[codex driver: mcp_servers]
  config --> connect[harness or gopi CLI: mcp.Connect]
  claude --> cc[Claude Code connects]
  codex --> cx[Codex connects]
  connect --> tools[mcp.Tools: one tool for each tool of a server]
  tools --> toolset[agent.Toolset]
  toolset -- the model calls a tool --> calltool[session.CallTool]
  calltool --> server[MCP server]
```

## Design

- One type, `agent.MCPServer`, names a server for each driver. It has the fields of an entry of an mcp.json file, so the file of the CLI, the config of a session, and the arguments of Claude Code and Codex hold the same data.
- A CLI agent connects to its own servers, so gopi holds no MCP session for it. The servers of the settings of the CLI stay.
- `pkg/mcp` is in the root module, because the harness connects with it. The cost: each caller of gopi depends on the MCP SDK.
- `mcp.Tools` returns tools from `tool.Func`, so the agent and the providers need no MCP code.
- The harness connects at `New` or `Durable`, and `agent.Agent.Close` closes the sessions. A subagent of the agent tool shares the sessions of its parent, so an agent call opens no connection.
- `mcp.Connect` returns the tools of each server that connected, also with an error. The harness then fails the start of the agent, because a config that names a server expects its tools. The CLI writes a warning and continues, so one server that is down does not stop the work.
- `agent.Toolset` is fixed at `agent.New` or `agent.Durable`, so a change of the tool list that a server announces does not reach the model.
- `mcp.Connect` defers each tool, because one server can have many tools. The search tool loads them, as in [Tool Change](../agent/tool-change.md).
- The fence does not confine an MCP server, so a stdio server can write any file and use the network.

## Map

| Task                                       | File                         |
| ------------------------------------------ | ---------------------------- |
| Change how an MCP tool becomes a tool      | `pkg/mcp/mcp.go`             |
| Change how a result becomes content        | `pkg/mcp/mcp.go`             |
| Add a transport, or change the connect     | `pkg/mcp/connect.go`         |
| Change the fields of a server              | `pkg/agent/mcp.go`           |
| Change how Claude Code gets the servers    | `pkg/agent/claude/claude.go` |
| Change how Codex gets the servers          | `pkg/agent/codex/codex.go`   |
| Change when the harness connects           | `pkg/harness/harness.go`     |
| Change the format of the mcp.json file     | `cmd/gopi/mcp.go`            |

## Related Concepts

- [Tool](tool.md)
- [Tool Change](../agent/tool-change.md)
- [Fence](../sandbox/fence.md)
