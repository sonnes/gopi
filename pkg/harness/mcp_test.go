package harness_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/harness"
	"github.com/sonnes/gopi/pkg/harness/permission"
)

type mcpEchoInput struct {
	Message string `json:"message"`
}

// mcpServer is an HTTP MCP server with the tool echo. It counts the
// sessions that a client closed.
type mcpServer struct {
	*httptest.Server
	closed atomic.Int32
}

func newMCPServer(t *testing.T) *mcpServer {
	t.Helper()
	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "test"}, nil)
	mcpsdk.AddTool(
		server,
		&mcpsdk.Tool{Name: "echo", Description: "Echo a message."},
		func(_ context.Context, _ *mcpsdk.CallToolRequest, in mcpEchoInput) (*mcpsdk.CallToolResult, any, error) {
			res := &mcpsdk.CallToolResult{
				Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: in.Message}},
			}
			return res, nil, nil
		},
	)
	handler := mcpsdk.NewStreamableHTTPHandler(
		func(*http.Request) *mcpsdk.Server { return server },
		nil,
	)
	s := &mcpServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			s.closed.Add(1)
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *mcpServer) config() agent.MCPServer {
	return agent.MCPServer{
		Name: "lw",
		Type: agent.MCPHTTP,
		URL:  s.URL,
	}
}

// lastToolResult returns the last tool result message of the log of a.
func lastToolResult(t *testing.T, a *agent.Agent) ai.Message {
	t.Helper()
	msgs := a.Log().Messages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == ai.RoleToolResult {
			return msgs[i]
		}
	}
	t.Fatal("no tool result in the log")
	return ai.Message{}
}

func TestStartConnectsTheMCPServers(t *testing.T) {
	srv := newMCPServer(t)
	p := &script{responses: []ai.Message{
		callTool("c1", "tool_search", `{"query":"select:mcp__lw__echo"}`),
		callTool("c2", "mcp__lw__echo", `{"message":"hi"}`),
		reply("done"),
	}}
	h := harness.New(with(base(t, p), harness.Mode(permission.Bypass))...)

	a := start(t, h, agent.Config{
		Session: "s1",
		MCP:     []agent.MCPServer{srv.config()},
	})
	run(t, a, "hi")

	assert.Equal(t, "hi", lastToolResult(t, a).Text(), "the tool of the MCP server runs")

	require.NoError(t, a.Close())
	assert.Equal(t, int32(1), srv.closed.Load(), "Close closes the MCP session")
}

func TestMCPOption(t *testing.T) {
	srv := newMCPServer(t)
	p := &script{responses: []ai.Message{reply("done")}}
	h := harness.New(base(t, p)...)

	a, err := h.New(context.Background(), harness.MCP(srv.config()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.Close() })
	run(t, a, "hi")

	assert.Contains(t, toolNames(a), "tool_search", "the MCP tools are deferred behind the search")
}

func TestStartFailsIfAnMCPServerDoesNotConnect(t *testing.T) {
	p := &script{}
	h := harness.New(base(t, p)...)

	_, err := h.Start(context.Background(), agent.Config{
		Session: "s1",
		MCP:     []agent.MCPServer{{Name: "broken", Command: "gopi-test-no-such-command"}},
	})

	assert.ErrorContains(t, err, `mcp server "broken"`)
}
