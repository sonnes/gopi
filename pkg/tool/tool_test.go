package tool_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/tool"
)

type echoInput struct {
	Message string `json:"message"`
}

type echoOutput struct {
	Echo string `json:"echo"`
}

func echoMessage(_ context.Context, in echoInput) (string, error) {
	return in.Message, nil
}

func TestNew(t *testing.T) {
	tests := []struct {
		name  string
		tool  tool.Tool
		input string
		want  ai.ToolResult
	}{
		{
			name:  "string output",
			tool:  tool.New("echo", echoMessage, tool.Description("Echo the message")),
			input: `{"message":"hi"}`,
			want:  ai.ToolResult{Content: []ai.Content{ai.Text{Text: "hi"}}},
		},
		{
			name: "struct output",
			tool: tool.New("echo", func(_ context.Context, in echoInput) (echoOutput, error) {
				return echoOutput{Echo: in.Message}, nil
			}),
			input: `{"message":"hi"}`,
			want:  ai.ToolResult{Content: []ai.Content{ai.Text{Text: `{"echo":"hi"}`}}},
		},
		{
			name:  "invalid input",
			tool:  tool.New("echo", echoMessage, tool.Description("Echo the message")),
			input: `{"message":`,
			want: ai.ToolResult{
				Content: []ai.Content{ai.Text{Text: "invalid input: unexpected end of JSON input"}},
				IsError: true,
			},
		},
		{
			name: "function error",
			tool: tool.New("echo", func(_ context.Context, in echoInput) (string, error) {
				return "", errors.New("no such city")
			}),
			input: `{"message":"hi"}`,
			want: ai.ToolResult{
				Content: []ai.Content{ai.Text{Text: "no such city"}},
				IsError: true,
			},
		},
		{
			name: "tool result output",
			tool: tool.New("echo", func(_ context.Context, in echoInput) (ai.ToolResult, error) {
				return ai.ToolResult{Content: []ai.Content{ai.Text{Text: "raw"}}}, nil
			}),
			input: `{"message":"hi"}`,
			want:  ai.ToolResult{Content: []ai.Content{ai.Text{Text: "raw"}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			call := ai.ToolCall{ID: "c1", Name: "echo", Input: json.RawMessage(tt.input)}
			got, err := tt.tool.Run(t.Context(), call)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNew_Info(t *testing.T) {
	tests := []struct {
		name string
		opts []tool.Option
		want ai.ToolInfo
	}{
		{
			name: "no options",
			want: ai.ToolInfo{Name: "echo"},
		},
		{
			name: "description",
			opts: []tool.Option{tool.Description("Echo the message")},
			want: ai.ToolInfo{Name: "echo", Description: "Echo the message"},
		},
		{
			name: "deferred",
			opts: []tool.Option{tool.Deferred()},
			want: ai.ToolInfo{Name: "echo", Deferred: true},
		},
		{
			name: "sequential",
			opts: []tool.Option{tool.Sequential()},
			want: ai.ToolInfo{Name: "echo", Sequential: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := tool.New("echo", echoMessage, tt.opts...).Info()
			require.NotNil(t, info.InputSchema, "New derives the schema from the input")
			assert.Contains(t, info.InputSchema.Properties, "message")

			info.InputSchema = nil
			assert.Equal(t, tt.want, info)
		})
	}
}

func TestNew_CanceledContext(t *testing.T) {
	wait := tool.New("wait", func(ctx context.Context, in echoInput) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := wait.Run(ctx, ai.ToolCall{ID: "c1", Name: "wait", Input: json.RawMessage(`{}`)})
	assert.ErrorIs(t, err, context.Canceled)
}

func pong(_ context.Context, call ai.ToolCall) (ai.ToolResult, error) {
	return ai.ToolResult{Content: []ai.Content{ai.Text{Text: "pong " + call.ID}}}, nil
}

func TestFunc(t *testing.T) {
	schema := &jsonschema.Schema{Type: "object"}
	ping := tool.Func("ping", pong,
		tool.Description("Ping"),
		tool.InputSchema(schema),
		tool.Deferred(),
	)

	assert.Equal(t, ai.ToolInfo{
		Name:        "ping",
		Description: "Ping",
		InputSchema: schema,
		Deferred:    true,
	}, ping.Info())

	got, err := ping.Run(t.Context(), ai.ToolCall{ID: "c1"})
	require.NoError(t, err)
	assert.Equal(t, "pong c1", got.Content[0].(ai.Text).Text)
}

func TestWrap(t *testing.T) {
	schema := &jsonschema.Schema{Type: "object"}
	ping := tool.Func("ping", pong,
		tool.Description("Ping"),
		tool.InputSchema(schema),
		tool.Deferred(),
	)

	wrapped := tool.Wrap(ping, func(ctx context.Context, call ai.ToolCall) (ai.ToolResult, error) {
		return ping.Run(ctx, ai.ToolCall{ID: "wrapped " + call.ID})
	})
	assert.Equal(t, ping.Info(), wrapped.Info(), "Wrap keeps the info of the tool")

	got, err := wrapped.Run(t.Context(), ai.ToolCall{ID: "c1"})
	require.NoError(t, err)
	assert.Equal(t, "pong wrapped c1", got.Content[0].(ai.Text).Text)

	renamed := tool.Wrap(ping, ping.Run, func(info *ai.ToolInfo) { info.Name = "mcp_ping" })
	assert.Equal(t, "mcp_ping", renamed.Info().Name, "an option edits the info")
	assert.True(t, renamed.Info().Deferred)
}

func TestDefer(t *testing.T) {
	echo := tool.New("echo", echoMessage, tool.Description("Echo the message"))
	deferred := tool.Defer(echo)

	want := echo.Info()
	want.Deferred = true
	assert.Equal(t, want, deferred.Info())

	call := ai.ToolCall{ID: "c1", Input: json.RawMessage(`{"message":"hi"}`)}
	got, err := deferred.Run(t.Context(), call)
	require.NoError(t, err)
	assert.Equal(t, "hi", got.Content[0].(ai.Text).Text, "a deferred tool runs as before")
}

// rename is a middleware that changes the name of a tool. It must be
// able to, because a bridge renames every tool that it imports.
func rename(name string) tool.Middleware {
	return func(next tool.Tool) tool.Tool {
		return tool.Wrap(next, next.Run, func(info *ai.ToolInfo) { info.Name = name })
	}
}

// deny is a middleware that answers with an error result and never calls
// the next tool.
func deny(reason string) tool.Middleware {
	return func(next tool.Tool) tool.Tool {
		run := func(context.Context, ai.ToolCall) (ai.ToolResult, error) {
			return ai.ToolResult{
				Content: []ai.Content{ai.Text{Text: reason}},
				IsError: true,
			}, nil
		}
		return tool.Wrap(next, run)
	}
}

// tag is a middleware that appends a text block to the result. Two tags
// in a chain show the order in which the chain applied them.
func tag(label string) tool.Middleware {
	return func(next tool.Tool) tool.Tool {
		run := func(ctx context.Context, call ai.ToolCall) (ai.ToolResult, error) {
			res, err := next.Run(ctx, call)
			if err != nil {
				return res, err
			}
			res.Content = append(res.Content, ai.Text{Text: label})
			return res, nil
		}
		return tool.Wrap(next, run)
	}
}

func TestChain_Rename(t *testing.T) {
	renamed := tool.Chain(newEchoTool(), rename("mcp_echo"))
	assert.Equal(t, "mcp_echo", renamed.Info().Name)

	call := ai.ToolCall{ID: "c1", Input: json.RawMessage(`{"message":"hi"}`)}
	got, err := renamed.Run(t.Context(), call)
	require.NoError(t, err)
	assert.Equal(t, "hi", got.Content[0].(ai.Text).Text)
}

func TestChain_KeepsDeferred(t *testing.T) {
	renamed := tool.Chain(tool.Defer(newEchoTool()), rename("mcp_echo"))
	assert.True(t, renamed.Info().Deferred, "a middleware that wraps keeps the flag")
}

func TestChain_Deny(t *testing.T) {
	called := false
	run := func(context.Context, ai.ToolCall) (ai.ToolResult, error) {
		called = true
		return ai.ToolResult{}, nil
	}
	inner := tool.Func("rm", run)

	got, err := tool.Chain(inner, deny("not allowed")).Run(t.Context(), ai.ToolCall{ID: "c1"})
	require.NoError(t, err)
	assert.True(t, got.IsError)
	assert.Equal(t, "not allowed", got.Content[0].(ai.Text).Text)
	assert.False(t, called)
}

func TestChain_Order(t *testing.T) {
	chained := tool.Chain(newEchoTool(), tag("outer"), tag("inner"))

	call := ai.ToolCall{ID: "c1", Input: json.RawMessage(`{"message":"hi"}`)}
	got, err := chained.Run(t.Context(), call)
	require.NoError(t, err)

	var texts []string
	for _, c := range got.Content {
		texts = append(texts, c.(ai.Text).Text)
	}
	// The inner middleware appends first, then the outer one.
	assert.Equal(t, []string{"hi", "inner", "outer"}, texts)
}

func TestChain_Empty(t *testing.T) {
	echo := newEchoTool()
	assert.Equal(t, echo.Info(), tool.Chain(echo).Info())
}

func newEchoTool() tool.Tool {
	return tool.New("echo", echoMessage, tool.Description("Echo the message"))
}
