package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/google/jsonschema-go/jsonschema"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/tool"
)

// Option sets an option of [Tools].
type Option func(*config)

type config struct {
	prefix   string
	deferred bool
}

// maxNameLen is the longest tool name that each provider accepts.
const maxNameLen = 64

// badNameChar matches each character that a provider rejects in a tool
// name. The MCP spec permits more characters, such as the dot.
var badNameChar = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// Prefix puts p before the name of each tool. The default is no prefix.
func Prefix(p string) Option {
	return func(c *config) { c.prefix = p }
}

// Deferred marks each tool as deferred, so the model loads it with the
// search tool of the agent. A server can have many tools, and a
// deferred tool costs no tokens until the model loads it. The default
// is not deferred.
func Deferred() Option {
	return func(c *config) { c.deferred = true }
}

// Tools returns one [tool.Tool] for each tool of the server of s, in the
// order of the server. Each tool keeps the description and the input
// schema of the server.
//
// The name of a tool is the prefix and the name on the server, with
// each character that a provider rejects changed to "_" and cut to 64
// bytes. If two tools then have the same name, Tools returns an error,
// because the model calls a tool by its name.
//
// A tool result with isError set becomes an error result, so the model
// reads it and can correct the call. A failure of the call, such as a
// closed session, becomes a Go error.
func Tools(ctx context.Context, s *mcpsdk.ClientSession, opts ...Option) ([]tool.Tool, error) {
	var cfg config
	for _, opt := range opts {
		opt(&cfg)
	}

	var tools []tool.Tool
	// seen maps each clean name to the name of the tool on the server.
	seen := make(map[string]string)
	for t, err := range s.Tools(ctx, nil) {
		if err != nil {
			return nil, fmt.Errorf("mcp: list tools: %w", err)
		}

		name := toolName(cfg.prefix, t.Name)
		if other, ok := seen[name]; ok {
			return nil, fmt.Errorf("mcp: tools %s and %s have the same name %s", other, t.Name, name)
		}
		seen[name] = t.Name

		schema, err := toSchema(t.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("mcp: input schema for tool %s: %w", t.Name, err)
		}

		topts := []tool.Option{
			tool.Description(t.Description),
			tool.InputSchema(schema),
		}
		if cfg.deferred {
			topts = append(topts, tool.Deferred())
		}
		tools = append(tools, tool.Func(
			name,
			runner(s, t.Name),
			topts...,
		))
	}
	return tools, nil
}

// toolName returns prefix+name in the form that each provider accepts.
func toolName(prefix, name string) string {
	n := badNameChar.ReplaceAllString(prefix+name, "_")
	if len(n) > maxNameLen {
		n = n[:maxNameLen]
	}
	return n
}

// toSchema decodes the input schema of a tool. A client session decodes
// the schema as a generic JSON value, so toSchema encodes it again and
// decodes it as a [jsonschema.Schema].
func toSchema(v any) (*jsonschema.Schema, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	var schema jsonschema.Schema
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, err
	}
	return &schema, nil
}

// runner returns the run function of the tool name of s. A call with
// no input sends an empty object, because a server can reject null.
func runner(s *mcpsdk.ClientSession, name string) func(context.Context, ai.ToolCall) (ai.ToolResult, error) {
	return func(ctx context.Context, call ai.ToolCall) (ai.ToolResult, error) {
		args := call.Input
		if len(args) == 0 {
			args = json.RawMessage(`{}`)
		}
		params := &mcpsdk.CallToolParams{
			Name:      name,
			Arguments: args,
		}
		res, err := s.CallTool(ctx, params)
		if err != nil {
			return ai.ToolResult{}, fmt.Errorf("mcp: call tool %s: %w", name, err)
		}

		content, err := toContent(res)
		if err != nil {
			return ai.ToolResult{}, fmt.Errorf("mcp: result of tool %s: %w", name, err)
		}
		return ai.ToolResult{
			Content: content,
			IsError: res.IsError,
		}, nil
	}
}

// toContent turns the content of an MCP result into blocks for the
// model. Text and images keep their type, and a text resource gives its
// text. The model gets each other type, such as audio or a binary
// resource, as its JSON text, because [ai.Content] has no block for it.
//
// If the result has no content, the model gets the JSON text of its
// structured content. The MCP spec asks a server to send both, but some
// servers send only the structured content.
func toContent(res *mcpsdk.CallToolResult) ([]ai.Content, error) {
	if len(res.Content) == 0 && res.StructuredContent != nil {
		data, err := json.Marshal(res.StructuredContent)
		if err != nil {
			return nil, err
		}
		return []ai.Content{ai.Text{Text: string(data)}}, nil
	}

	var out []ai.Content
	for _, c := range res.Content {
		if text, ok := resourceText(c); ok {
			out = append(out, ai.Text{Text: text})
			continue
		}
		switch c := c.(type) {
		case *mcpsdk.TextContent:
			out = append(out, ai.Text{Text: c.Text})
		case *mcpsdk.ImageContent:
			out = append(out, ai.File{
				MediaType: c.MIMEType,
				Data:      c.Data,
			})
		default:
			data, err := json.Marshal(c)
			if err != nil {
				return nil, err
			}
			out = append(out, ai.Text{Text: string(data)})
		}
	}
	return out, nil
}

// resourceText returns the text of c if c is a text resource.
func resourceText(c mcpsdk.Content) (string, bool) {
	r, ok := c.(*mcpsdk.EmbeddedResource)
	if !ok || r.Resource == nil || r.Resource.Text == "" {
		return "", false
	}
	return r.Resource.Text, true
}
