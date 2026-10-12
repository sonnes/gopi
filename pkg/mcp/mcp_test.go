package mcp_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/mcp"
	"github.com/sonnes/gopi/pkg/tool"
)

type echoInput struct {
	Message string `json:"message" jsonschema:"the text to echo"`
}

// addEcho adds a tool that returns its message as text.
func addEcho(s *mcpsdk.Server) {
	mcpsdk.AddTool(
		s,
		&mcpsdk.Tool{Name: "echo", Description: "Echo a message."},
		func(_ context.Context, _ *mcpsdk.CallToolRequest, in echoInput) (*mcpsdk.CallToolResult, any, error) {
			res := &mcpsdk.CallToolResult{
				Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: in.Message}},
			}
			return res, nil, nil
		},
	)
}

// addFail adds a tool that always fails.
func addFail(s *mcpsdk.Server) {
	mcpsdk.AddTool(
		s,
		&mcpsdk.Tool{Name: "fail", Description: "Fail."},
		func(context.Context, *mcpsdk.CallToolRequest, struct{}) (*mcpsdk.CallToolResult, any, error) {
			return nil, nil, errors.New("no data")
		},
	)
}

// addImage adds a tool that returns one PNG image.
func addImage(s *mcpsdk.Server) {
	mcpsdk.AddTool(
		s,
		&mcpsdk.Tool{Name: "image", Description: "Return an image."},
		func(context.Context, *mcpsdk.CallToolRequest, struct{}) (*mcpsdk.CallToolResult, any, error) {
			res := &mcpsdk.CallToolResult{
				Content: []mcpsdk.Content{&mcpsdk.ImageContent{
					MIMEType: "image/png",
					Data:     []byte("png"),
				}},
			}
			return res, nil, nil
		},
	)
}

// addRaw adds a tool with the name name that returns res. The tool
// does not decode its input.
func addRaw(name string, res *mcpsdk.CallToolResult) func(*mcpsdk.Server) {
	return func(s *mcpsdk.Server) {
		s.AddTool(
			&mcpsdk.Tool{
				Name:        name,
				InputSchema: map[string]any{"type": "object"},
			},
			func(context.Context, *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
				return res, nil
			},
		)
	}
}

// addArgs adds a tool that returns the raw JSON of its arguments as
// text.
func addArgs(s *mcpsdk.Server) {
	s.AddTool(
		&mcpsdk.Tool{
			Name:        "args",
			InputSchema: map[string]any{"type": "object"},
		},
		func(_ context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			res := &mcpsdk.CallToolResult{
				Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(req.Params.Arguments)}},
			}
			return res, nil
		},
	)
}

// newSession connects a client to an in-memory server with the tools
// that add adds.
func newSession(t *testing.T, add ...func(*mcpsdk.Server)) *mcpsdk.ClientSession {
	t.Helper()
	ct, st := mcpsdk.NewInMemoryTransports()

	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "test"}, nil)
	for _, a := range add {
		a(server)
	}
	_, err := server.Connect(t.Context(), st, nil)
	require.NoError(t, err)

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "gopi"}, nil)
	session, err := client.Connect(t.Context(), ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// find returns the tool with the name name.
func find(t *testing.T, tools []tool.Tool, name string) tool.Tool {
	t.Helper()
	for _, tl := range tools {
		if tl.Info().Name == name {
			return tl
		}
	}
	require.Failf(t, "no tool", "no tool named %s", name)
	return nil
}

func TestTools(t *testing.T) {
	tests := []struct {
		name         string
		opts         []mcp.Option
		wantName     string
		wantDeferred bool
	}{
		{
			name:     "no options",
			wantName: "echo",
		},
		{
			name:     "prefix",
			opts:     []mcp.Option{mcp.Prefix("mcp__test__")},
			wantName: "mcp__test__echo",
		},
		{
			name:         "deferred",
			opts:         []mcp.Option{mcp.Deferred()},
			wantName:     "echo",
			wantDeferred: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := newSession(t, addEcho)

			tools, err := mcp.Tools(t.Context(), session, tt.opts...)
			require.NoError(t, err)
			require.Len(t, tools, 1)

			info := tools[0].Info()
			assert.Equal(t, tt.wantName, info.Name)
			assert.Equal(t, "Echo a message.", info.Description)
			assert.Equal(t, tt.wantDeferred, info.Deferred)
			require.NotNil(t, info.InputSchema)
			assert.Equal(t, "the text to echo", info.InputSchema.Properties["message"].Description)
		})
	}
}

func TestToolsNames(t *testing.T) {
	long := strings.Repeat("a", 70)
	empty := &mcpsdk.CallToolResult{}

	tests := []struct {
		name   string
		tool   string
		prefix string
		want   string
	}{
		{
			name: "valid name",
			tool: "get-issue_2",
			want: "get-issue_2",
		},
		{
			name: "dot",
			tool: "github.create_issue",
			want: "github_create_issue",
		},
		{
			name:   "prefix with a dot",
			tool:   "echo",
			prefix: "mcp__my.server__",
			want:   "mcp__my_server__echo",
		},
		{
			name: "long name",
			tool: long,
			want: long[:64],
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := newSession(t, addRaw(tt.tool, empty))

			tools, err := mcp.Tools(t.Context(), session, mcp.Prefix(tt.prefix))
			require.NoError(t, err)
			require.Len(t, tools, 1)
			assert.Equal(t, tt.want, tools[0].Info().Name)
		})
	}
}

func TestToolsSameName(t *testing.T) {
	empty := &mcpsdk.CallToolResult{}
	session := newSession(t, addRaw("a.x", empty), addRaw("a_x", empty))

	_, err := mcp.Tools(t.Context(), session)
	assert.ErrorContains(t, err, "a_x")
}

func TestToolRunCleanName(t *testing.T) {
	session := newSession(t, addEcho, addRaw("a.b", &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "ok"}},
	}))
	tools, err := mcp.Tools(t.Context(), session)
	require.NoError(t, err)

	call := ai.ToolCall{Name: "a_b", Input: []byte(`{}`)}
	got, err := find(t, tools, "a_b").Run(t.Context(), call)
	require.NoError(t, err)
	assert.Equal(t, ai.ToolResult{Content: []ai.Content{ai.Text{Text: "ok"}}}, got)
}

func TestToolRun(t *testing.T) {
	tests := []struct {
		name  string
		tool  string
		input string
		want  ai.ToolResult
	}{
		{
			name:  "text",
			tool:  "echo",
			input: `{"message":"hi"}`,
			want:  ai.ToolResult{Content: []ai.Content{ai.Text{Text: "hi"}}},
		},
		{
			name:  "error result",
			tool:  "fail",
			input: `{}`,
			want: ai.ToolResult{
				Content: []ai.Content{ai.Text{Text: "no data"}},
				IsError: true,
			},
		},
		{
			name:  "image",
			tool:  "image",
			input: `{}`,
			want: ai.ToolResult{Content: []ai.Content{ai.File{
				MediaType: "image/png",
				Data:      []byte("png"),
			}}},
		},
		{
			name: "no input",
			tool: "args",
			want: ai.ToolResult{Content: []ai.Content{ai.Text{Text: "{}"}}},
		},
		{
			name:  "structured content only",
			tool:  "structured",
			input: `{}`,
			want:  ai.ToolResult{Content: []ai.Content{ai.Text{Text: `{"count":2}`}}},
		},
		{
			name:  "text resource",
			tool:  "resource",
			input: `{}`,
			want:  ai.ToolResult{Content: []ai.Content{ai.Text{Text: "hello"}}},
		},
		{
			name:  "audio",
			tool:  "audio",
			input: `{}`,
			want: ai.ToolResult{Content: []ai.Content{ai.Text{
				Text: `{"type":"audio","mimeType":"audio/wav","data":"d2F2"}`,
			}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := newSession(
				t,
				addEcho,
				addFail,
				addImage,
				addArgs,
				addRaw("structured", &mcpsdk.CallToolResult{
					StructuredContent: map[string]any{"count": 2},
				}),
				addRaw("resource", &mcpsdk.CallToolResult{
					Content: []mcpsdk.Content{&mcpsdk.EmbeddedResource{
						Resource: &mcpsdk.ResourceContents{
							URI:      "file:///notes.txt",
							MIMEType: "text/plain",
							Text:     "hello",
						},
					}},
				}),
				addRaw("audio", &mcpsdk.CallToolResult{
					Content: []mcpsdk.Content{&mcpsdk.AudioContent{
						MIMEType: "audio/wav",
						Data:     []byte("wav"),
					}},
				}),
			)
			tools, err := mcp.Tools(t.Context(), session, mcp.Prefix("p__"))
			require.NoError(t, err)

			var input []byte
			if tt.input != "" {
				input = []byte(tt.input)
			}
			call := ai.ToolCall{Name: "p__" + tt.tool, Input: input}
			got, err := find(t, tools, "p__"+tt.tool).Run(t.Context(), call)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestToolRunClosedSession(t *testing.T) {
	session := newSession(t, addEcho)
	tools, err := mcp.Tools(t.Context(), session)
	require.NoError(t, err)
	require.NoError(t, session.Close())

	call := ai.ToolCall{Name: "echo", Input: []byte(`{"message":"hi"}`)}
	_, err = tools[0].Run(t.Context(), call)
	assert.Error(t, err)
}
