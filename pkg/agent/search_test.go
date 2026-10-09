package agent_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/internal/emittest"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/tool"
)

type tickerInput struct {
	Symbol string `json:"symbol" jsonschema:"The ticker symbol"`
}

var stock = tool.New("stock_price",
	func(_ context.Context, in tickerInput) (string, error) {
		return "42 for " + in.Symbol, nil
	},
	tool.Description("Get the price of a stock."),
	tool.Deferred(),
)

// search runs an agent with echo and the deferred tools get_weather and
// stock_price over the script p. It returns the messages of the run.
func search(t *testing.T, p *script) []ai.Message {
	t.Helper()
	a := agent.New(model(p), agent.Toolset(echo, weather, stock))
	events, msgs, err := collect(t, a.Run(context.Background(), ai.UserMessage("hi")))
	require.NoError(t, err)
	emittest.CheckEvents(t, events)
	return msgs
}

// toolResults returns the tool result messages of msgs.
func toolResults(msgs []ai.Message) []ai.Message {
	var out []ai.Message
	for _, m := range msgs {
		if m.Role == ai.RoleToolResult {
			out = append(out, m)
		}
	}
	return out
}

func TestSearchLoadsTheMatches(t *testing.T) {
	p := &script{responses: []ai.Message{
		toolCall("c1", "tool_search", `{"query":"weather forecast"}`),
		toolCall("c2", "get_weather", `{"city":"Paris"}`),
		text("done"),
	}}
	msgs := search(t, p)

	for i, opts := range p.opts {
		assert.Equal(t, []string{"echo", "tool_search"}, toolNames(opts),
			"call %d sends the tools that are not deferred, and the same tools as the first call", i)
	}

	calls := p.received()
	require.Len(t, calls, 3)
	change := calls[1][len(calls[1])-1]
	assert.Equal(t, ai.RoleSystem, change.Role, "the change comes after the tool result")
	require.Len(t, change.Content, 1)
	assert.Equal(t, "get_weather", change.Content[0].(ai.ToolAdded).Tool.Name)

	results := toolResults(msgs)
	require.Len(t, results, 2)
	assert.False(t, results[0].IsError)
	assert.Contains(t, results[0].Text(), "get_weather")
	assert.NotContains(t, results[0].Text(), "stock_price")
	assert.False(t, results[1].IsError, "the model calls the loaded tool")
	assert.Equal(t, "Sunny in Paris", results[1].Text())
}

func TestSearchOutlivesTheRun(t *testing.T) {
	ctx := context.Background()
	p := &script{responses: []ai.Message{
		toolCall("c1", "tool_search", `{"query":"weather"}`),
		toolCall("c2", "get_weather", `{"city":"Paris"}`),
		text("done"),
	}}
	failed := false
	stopOnce := agent.AfterTurn(func(context.Context, agent.State) ([]ai.Message, error) {
		if failed {
			return nil, nil
		}
		failed = true
		return nil, errors.New("stop")
	})
	a := agent.New(model(p), agent.Toolset(echo, weather), stopOnce)

	_, _, err := collect(t, a.Run(ctx, ai.UserMessage("weather?")))
	require.Error(t, err, "the run ends after the search and before the next model call")
	_, msgs, err := collect(t, a.Run(ctx, ai.UserMessage("go on")))
	require.NoError(t, err)

	results := toolResults(msgs)
	require.Len(t, results, 1)
	assert.Equal(t, "Sunny in Paris", results[0].Text(), "the next run declares a tool that a run loaded")
}

func TestSearchResult(t *testing.T) {
	tests := []struct {
		name     string
		call     ai.Message
		wantErr  bool
		wantText string
		// notText is a tool name that the result must not hold.
		notText string
		// noChange is set if the run must add no system message.
		noChange bool
	}{
		{
			name:     "select loads the exact names",
			call:     toolCall("c1", "tool_search", `{"query":"select:stock_price"}`),
			wantText: "stock_price",
			notText:  "get_weather",
		},
		{
			name:     "select reports a name that is not deferred",
			call:     toolCall("c1", "tool_search", `{"query":"select:get_weather,echo"}`),
			wantText: "No deferred tool is named echo",
		},
		{
			name:    "select with no name is an error",
			call:    toolCall("c1", "tool_search", `{"query":"select:"}`),
			wantErr: true,
		},
		{
			name:     "a keyword matches the schema",
			call:     toolCall("c1", "tool_search", `{"query":"ticker symbol"}`),
			wantText: "stock_price",
		},
		{
			name:     "a word of the name counts more than a word of the text",
			call:     toolCall("c1", "tool_search", `{"query":"price get","max_results":1}`),
			wantText: "stock_price",
			notText:  "get_weather",
		},
		{
			name:     "a tie keeps the order of Toolset",
			call:     toolCall("c1", "tool_search", `{"query":"get","max_results":1}`),
			wantText: "Loaded 1 tool",
			notText:  "stock_price",
		},
		{
			name:     "a required word keeps only the names with it",
			call:     toolCall("c1", "tool_search", `{"query":"+stock"}`),
			wantText: "stock_price",
			notText:  "get_weather",
		},
		{
			name:     "a search with no match changes no tool",
			call:     toolCall("c1", "tool_search", `{"query":"zebra"}`),
			wantText: "No tool matches",
			noChange: true,
		},
		{
			name:    "an empty query is an error that the model can correct",
			call:    toolCall("c1", "tool_search", `{"query":""}`),
			wantErr: true,
		},
		{
			name:     "the model cannot call a deferred tool before a search loads it",
			call:     toolCall("c1", "get_weather", `{"city":"Paris"}`),
			wantErr:  true,
			wantText: "Load it with tool_search",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &script{responses: []ai.Message{tt.call, text("done")}}
			msgs := search(t, p)

			results := toolResults(msgs)
			require.Len(t, results, 1)
			assert.Equal(t, tt.wantErr, results[0].IsError)
			assert.Contains(t, results[0].Text(), tt.wantText)
			if tt.notText != "" {
				assert.NotContains(t, results[0].Text(), tt.notText)
			}
			if !tt.noChange {
				return
			}
			for _, m := range afterBase(t, msgs) {
				assert.NotEqual(t, ai.RoleSystem, m.Role)
			}
		})
	}
}

func TestSearchListsTheDeferredTools(t *testing.T) {
	p := &script{responses: []ai.Message{text("done")}}
	search(t, p)

	require.NotEmpty(t, p.opts)
	tools, _ := ai.GetOption(p.opts[0], ai.Tools)
	i := slices.IndexFunc(tools, func(info ai.ToolInfo) bool { return info.Name == "tool_search" })
	require.GreaterOrEqual(t, i, 0)
	assert.True(t, strings.HasSuffix(tools[i].Description, "get_weather, stock_price."),
		"the description ends with the deferred tools, in the order of Toolset")
}

func TestToolNamedToolSearch(t *testing.T) {
	own := tool.New("tool_search", func(context.Context, echoInput) (string, error) {
		return "", nil
	})
	p := &script{}

	assert.Panics(t, func() {
		agent.New(model(p), agent.Toolset(own, weather))
	}, "a tool named tool_search clashes with the search of the agent")
}
