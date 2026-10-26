package agent

// MCPServer is one MCP server of an agent, in the shape of an entry of
// an mcp.json file. A driver gives the server to its agent: the CLI of
// Claude Code or Codex connects to it, and the harness connects with
// [github.com/sonnes/gopi/pkg/mcp.Connect]. The model sees each tool
// of the server as "mcp__<Name>__<tool>".
type MCPServer struct {
	// Name names the server in the names of its tools. It is required,
	// and it must be unique among the servers of one agent.
	Name string `json:"name"`
	// Type is [MCPStdio] or [MCPHTTP]. Empty is [MCPStdio].
	Type string `json:"type,omitempty"`

	// Command, Args, and Env start a stdio server. Env adds to the
	// environment of the process that starts the server.
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`

	// URL and Headers reach an HTTP server. Each request carries
	// Headers, such as a bearer token.
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// The types of an [MCPServer].
const (
	// MCPStdio starts the server as a process, and talks to it on its
	// stdin and stdout.
	MCPStdio = "stdio"
	// MCPHTTP reaches the server over the streamable HTTP transport.
	MCPHTTP = "http"
)
