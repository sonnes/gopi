// Package mcp gives the tools of an MCP server to an agent. [Tools]
// lists the tools of a connected [mcpsdk.ClientSession] and returns one
// [tool.Tool] for each. The agent runs them as it runs any other tool.
//
// A caller must obey these rules:
//
//   - Connect the session, and close it after the last run of the
//     agent. A tool calls the server through the session, so a closed
//     session fails each call.
//   - Give each server its own [Prefix] if the agent has more than one
//     server, because the model calls a tool by its name.
//
// The reasons are in docs/concepts/tool/mcp.md.
package mcp
