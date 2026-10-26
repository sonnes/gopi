package mcp_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/mcp"
)

// testServerEnv names the variable that makes the test binary act as a
// stdio MCP server. The value "echo" serves the echo tool. The value
// "fail" writes to stderr and exits.
const testServerEnv = "GOPI_MCP_TEST_SERVER"

func TestMain(m *testing.M) {
	switch os.Getenv(testServerEnv) {
	case "echo":
		serveStdio()
		return
	case "fail":
		fmt.Fprintln(os.Stderr, "Error: ROOT is not set")
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// serveStdio serves the echo tool on stdin and stdout. Each result
// starts with the value of GOPI_MCP_TEST_GREETING.
func serveStdio() {
	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "test"}, nil)
	mcpsdk.AddTool(
		server,
		&mcpsdk.Tool{Name: "echo", Description: "Echo a message."},
		func(_ context.Context, _ *mcpsdk.CallToolRequest, in echoInput) (*mcpsdk.CallToolResult, any, error) {
			text := os.Getenv("GOPI_MCP_TEST_GREETING") + in.Message
			res := &mcpsdk.CallToolResult{
				Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: text}},
			}
			return res, nil, nil
		},
	)
	if err := server.Run(context.Background(), &mcpsdk.StdioTransport{}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// testBinary returns the path of the test binary.
func testBinary(t *testing.T) string {
	t.Helper()
	bin, err := os.Executable()
	require.NoError(t, err)
	return bin
}

// newHTTPServer starts an HTTP MCP server with the echo tool. The
// server answers 401 to a request without the header Authorization:
// Bearer token.
func newHTTPServer(t *testing.T, token string) *httptest.Server {
	t.Helper()
	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "test"}, nil)
	addEcho(server)
	handler := mcpsdk.NewStreamableHTTPHandler(
		func(*http.Request) *mcpsdk.Server { return server },
		nil,
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// httpServer returns the config of the server srv under name, with the
// bearer token.
func httpServer(name string, srv *httptest.Server, token string) agent.MCPServer {
	return agent.MCPServer{
		Name:    name,
		Type:    agent.MCPHTTP,
		URL:     srv.URL,
		Headers: map[string]string{"Authorization": "Bearer " + token},
	}
}

func TestConnect(t *testing.T) {
	srv := newHTTPServer(t, "secret")
	servers := []agent.MCPServer{
		httpServer("remote", srv, "secret"),
		{Name: "broken", Command: "gopi-test-no-such-command"},
	}

	tools, closeMCP, err := mcp.Connect(t.Context(), servers, nil)
	t.Cleanup(func() { _ = closeMCP() })

	require.ErrorContains(t, err, `mcp server "broken"`)
	require.Len(t, tools, 1, "Connect returns the tools of each server that connected")
	info := tools[0].Info()
	assert.Equal(t, "mcp__remote__echo", info.Name)
	assert.True(t, info.Deferred)

	call := ai.ToolCall{Name: info.Name, Input: []byte(`{"message":"hi"}`)}
	got, err := tools[0].Run(t.Context(), call)
	require.NoError(t, err)
	assert.Equal(t, ai.ToolResult{Content: []ai.Content{ai.Text{Text: "hi"}}}, got)
}

func TestConnectStdio(t *testing.T) {
	servers := []agent.MCPServer{{
		Name:    "local",
		Command: testBinary(t),
		Args:    []string{"-test.run=^$"},
		Env:     map[string]string{"GOPI_MCP_TEST_GREETING": "hello "},
	}}
	// The server gets GOPI_MCP_TEST_SERVER from environ, not from the
	// environment of the test.
	environ := []string{testServerEnv + "=echo"}

	tools, closeMCP, err := mcp.Connect(t.Context(), servers, environ)
	require.NoError(t, err)
	t.Cleanup(func() { _ = closeMCP() })

	require.Len(t, tools, 1)
	call := ai.ToolCall{Name: "mcp__local__echo", Input: []byte(`{"message":"hi"}`)}
	got, err := tools[0].Run(t.Context(), call)
	require.NoError(t, err)
	assert.Equal(t, ai.ToolResult{Content: []ai.Content{ai.Text{Text: "hello hi"}}}, got)
}

func TestConnectSameName(t *testing.T) {
	srv := newHTTPServer(t, "secret")
	servers := []agent.MCPServer{
		httpServer("a.b", srv, "secret"),
		httpServer("a_b", srv, "secret"),
	}

	tools, closeMCP, err := mcp.Connect(t.Context(), servers, nil)
	t.Cleanup(func() { _ = closeMCP() })

	require.Len(t, tools, 1, "Connect keeps the first server")
	assert.Equal(t, "mcp__a_b__echo", tools[0].Info().Name)
	require.ErrorContains(t, err, `mcp server "a_b"`)
	assert.ErrorContains(t, err, `"a.b"`)
}

func TestConnectErrors(t *testing.T) {
	srv := newHTTPServer(t, "secret")
	tests := []struct {
		name    string
		server  agent.MCPServer
		environ []string
		wantErr string
	}{
		{
			name:    "wrong token",
			server:  httpServer("remote", srv, "wrong"),
			wantErr: `mcp server "remote"`,
		},
		{
			name:    "unknown type",
			server:  agent.MCPServer{Name: "old", Type: "sse", URL: srv.URL},
			wantErr: `mcp server "old": unknown type "sse"`,
		},
		{
			name:    "stderr of a stdio server",
			server:  agent.MCPServer{Name: "local", Command: testBinary(t)},
			environ: []string{testServerEnv + "=fail"},
			wantErr: "Error: ROOT is not set",
		},
		{
			name:    "no name",
			server:  httpServer("", srv, "secret"),
			wantErr: "a name is required",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tools, closeMCP, err := mcp.Connect(
				t.Context(),
				[]agent.MCPServer{tt.server},
				tt.environ,
			)
			t.Cleanup(func() { _ = closeMCP() })

			assert.Empty(t, tools)
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestConnectNoServers(t *testing.T) {
	tools, closeMCP, err := mcp.Connect(t.Context(), nil, nil)
	require.NoError(t, err)
	assert.Empty(t, tools)
	assert.NoError(t, closeMCP())
}
