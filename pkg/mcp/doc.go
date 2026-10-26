// Package mcp gives the tools of MCP servers to an agent. [Connect]
// connects to a list of [agent.MCPServer] values and returns their
// tools. [Tools] lists the tools of one connected
// [mcpsdk.ClientSession] and returns one [tool.Tool] for each. The
// agent runs an MCP tool as it runs any other tool.
//
// A caller must obey these rules:
//
//   - Close the sessions after the last run of the agent. A tool calls
//     the server through its session, so a closed session fails each
//     call.
//   - With [Tools], give each server its own [Prefix] if the agent has
//     more than one server, because the model calls a tool by its
//     name. [Connect] gives each server its prefix.
//
// The reasons are in docs/concepts/tool/mcp.md.
package mcp
