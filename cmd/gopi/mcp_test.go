package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
)

type echoInput struct {
	Message string `json:"message"`
}

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

// newMCPServer starts an HTTP MCP server with one tool, echo. The
// server answers 401 to a request without the header Authorization:
// Bearer token.
func newMCPServer(t *testing.T, token string) *httptest.Server {
	t.Helper()
	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "test"}, nil)
	mcpsdk.AddTool(
		server,
		&mcpsdk.Tool{Name: "echo", Description: "Echo a message."},
		func(_ context.Context, _ *mcpsdk.CallToolRequest, in echoInput) (*mcpsdk.CallToolResult, any, error) {
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

// writeFile writes data to a new file and returns its path.
func writeFile(t *testing.T, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mcp.json")
	require.NoError(t, os.WriteFile(path, []byte(data), 0o600))
	return path
}

func TestLoadMCP(t *testing.T) {
	srv := newMCPServer(t, "secret")
	path := writeFile(t, `{
  "mcpServers": {
    "remote": {
      "type": "http",
      "url": "${URL}",
      "headers": {"Authorization": "Bearer ${TOKEN}"}
    },
    "broken": {
      "command": "gopi-test-no-such-command"
    }
  }
}`)
	environ := []string{
		"URL=" + srv.URL,
		"TOKEN=secret",
	}

	var warn bytes.Buffer
	tools, closeMCP, err := loadMCP(
		t.Context(),
		path,
		environ,
		&warn,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = closeMCP() })

	require.Len(t, tools, 1)
	info := tools[0].Info()
	assert.Equal(t, "mcp__remote__echo", info.Name)
	assert.True(t, info.Deferred)
	assert.Contains(t, warn.String(), `mcp server "broken"`)

	call := ai.ToolCall{Name: info.Name, Input: []byte(`{"message":"hi"}`)}
	got, err := tools[0].Run(t.Context(), call)
	require.NoError(t, err)
	assert.Equal(t, ai.ToolResult{Content: []ai.Content{ai.Text{Text: "hi"}}}, got)
}

func TestLoadMCPStdio(t *testing.T) {
	path := writeFile(t, `{
  "mcpServers": {
    "local": {
      "command": "${BIN}",
      "args": ["-test.run=^$"],
      "env": {"GOPI_MCP_TEST_GREETING": "${GREETING}"}
    }
  }
}`)
	// The server gets GOPI_MCP_TEST_SERVER from environ, not from the
	// environment of the test.
	environ := []string{
		"BIN=" + testBinary(t),
		"GREETING=hello ",
		testServerEnv + "=echo",
	}

	var warn bytes.Buffer
	tools, closeMCP, err := loadMCP(t.Context(), path, environ, &warn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = closeMCP() })

	require.Empty(t, warn.String())
	require.Len(t, tools, 1)
	call := ai.ToolCall{Name: "mcp__local__echo", Input: []byte(`{"message":"hi"}`)}
	got, err := tools[0].Run(t.Context(), call)
	require.NoError(t, err)
	assert.Equal(t, ai.ToolResult{Content: []ai.Content{ai.Text{Text: "hello hi"}}}, got)
}

func TestLoadMCPStderr(t *testing.T) {
	path := writeFile(t, `{"mcpServers": {"local": {"command": "${BIN}"}}}`)
	environ := []string{
		"BIN=" + testBinary(t),
		testServerEnv + "=fail",
	}

	var warn bytes.Buffer
	tools, closeMCP, err := loadMCP(t.Context(), path, environ, &warn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = closeMCP() })

	assert.Empty(t, tools)
	assert.Contains(t, warn.String(), `mcp server "local"`)
	assert.Contains(t, warn.String(), "Error: ROOT is not set")
}

func TestLoadMCPSameName(t *testing.T) {
	srv := newMCPServer(t, "secret")
	server := `{"type": "http", "url": "${URL}", "headers": {"Authorization": "Bearer secret"}}`
	path := writeFile(t, `{"mcpServers": {"a.b": `+server+`, "a_b": `+server+`}}`)

	var warn bytes.Buffer
	tools, closeMCP, err := loadMCP(t.Context(), path, []string{"URL=" + srv.URL}, &warn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = closeMCP() })

	require.Len(t, tools, 1)
	assert.Equal(t, "mcp__a_b__echo", tools[0].Info().Name)
	assert.Contains(t, warn.String(), `mcp server "a_b"`)
	assert.Contains(t, warn.String(), `"a.b"`)
}

func TestLoadMCPWarnings(t *testing.T) {
	srv := newMCPServer(t, "secret")
	tests := []struct {
		name     string
		file     string
		wantWarn string
	}{
		{
			name:     "wrong token",
			file:     `{"mcpServers": {"remote": {"type": "http", "url": "` + srv.URL + `"}}}`,
			wantWarn: `mcp server "remote"`,
		},
		{
			name:     "unknown type",
			file:     `{"mcpServers": {"old": {"type": "sse", "url": "` + srv.URL + `"}}}`,
			wantWarn: `mcp server "old": unknown type "sse"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var warn bytes.Buffer
			tools, closeMCP, err := loadMCP(
				t.Context(),
				writeFile(t, tt.file),
				nil,
				&warn,
			)
			require.NoError(t, err)
			t.Cleanup(func() { _ = closeMCP() })

			assert.Empty(t, tools)
			assert.Contains(t, warn.String(), tt.wantWarn)
		})
	}
}

func TestLoadMCPErrors(t *testing.T) {
	tests := []struct {
		name    string
		path    func(t *testing.T) string
		wantErr string
	}{
		{
			name:    "no file",
			path:    func(t *testing.T) string { return filepath.Join(t.TempDir(), "mcp.json") },
			wantErr: "no such file",
		},
		{
			name:    "bad JSON",
			path:    func(t *testing.T) string { return writeFile(t, `{`) },
			wantErr: "mcp.json",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := loadMCP(
				t.Context(),
				tt.path(t),
				nil,
				&bytes.Buffer{},
			)
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestExpandEnv(t *testing.T) {
	getenv := envLookup([]string{"A=1"})

	tests := []struct {
		in   string
		want string
	}{
		{in: "${A}", want: "1"},
		{in: "x-${A}-${B}", want: "x-1-"},
		{in: "$A", want: "$A"},
		{in: "no vars", want: "no vars"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			assert.Equal(t, tt.want, expandEnv(tt.in, getenv))
		})
	}
}
