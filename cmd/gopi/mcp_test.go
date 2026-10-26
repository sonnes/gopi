package main

import (
	"bytes"
	"context"
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
