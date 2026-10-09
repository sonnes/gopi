package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/internal/emittest"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/tool"
)

// trace records the order in which hooks run. It is safe for
// concurrent use.
type trace struct {
	mu    sync.Mutex
	steps []string
}

func (tr *trace) add(s string) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.steps = append(tr.steps, s)
}

func (tr *trace) get() []string {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.steps
}

// resultTexts returns the text of each tool result in msgs.
func resultTexts(msgs []ai.Message) []string {
	var out []string
	for _, m := range msgs {
		if m.Role == ai.RoleToolResult {
			out = append(out, m.Text())
		}
	}
	return out
}

func TestBeforeCall(t *testing.T) {
	tests := []struct {
		name      string
		responses []ai.Message
		hooks     func(tr *trace) []agent.Option
		check     func(t *testing.T, p *script, a *agent.Agent, tr *trace, err error)
	}{
		{
			name:      "the chain runs in the order of registration",
			responses: []ai.Message{text("ok")},
			hooks: func(tr *trace) []agent.Option {
				return []agent.Option{
					agent.BeforeCall(func(_ context.Context, _ agent.State, r agent.Request) (agent.Request, error) {
						tr.add("first")
						r.Messages = append(r.Messages, ai.UserMessage("one"))
						return r, nil
					}),
					agent.BeforeCall(func(_ context.Context, _ agent.State, r agent.Request) (agent.Request, error) {
						tr.add("second")
						r.Messages = append(r.Messages, ai.UserMessage("two"))
						return r, nil
					}),
				}
			},
			check: func(t *testing.T, p *script, a *agent.Agent, tr *trace, err error) {
				require.NoError(t, err)
				assert.Equal(t, []string{"first", "second"}, tr.get())
				assert.Equal(t, []string{"hi", "one", "two"}, msgTexts(p.received()[0]))
				assert.Equal(t, []string{"hi", "ok"}, historyTexts(a), "the history does not change")
			},
		},
		{
			name: "an option for one turn",
			responses: []ai.Message{
				toolCall("c1", "echo", `{"s":"x"}`),
				text("done"),
			},
			hooks: func(tr *trace) []agent.Option {
				return []agent.Option{
					agent.BeforeCall(func(_ context.Context, s agent.State, r agent.Request) (agent.Request, error) {
						if s.Turn() == 2 {
							r.Options = ai.JoinOptions(r.Options, ai.Thinking(ai.ThinkingHigh))
						}
						return r, nil
					}),
				}
			},
			check: func(t *testing.T, p *script, _ *agent.Agent, _ *trace, err error) {
				require.NoError(t, err)
				require.Len(t, p.opts, 2)
				_, set := ai.GetOption(p.opts[0], ai.Thinking)
				assert.False(t, set, "turn 1 has no thinking level")
				thinking, _ := ai.GetOption(p.opts[1], ai.Thinking)
				assert.Equal(t, ai.ThinkingHigh, thinking)
				toolset, _ := ai.GetOption(p.opts[1], ai.Tools)
				assert.Len(t, toolset, 1, "the hook gets the Toolset")
			},
		},
		{
			name:      "an error fails the run before the model call",
			responses: []ai.Message{text("ok")},
			hooks: func(tr *trace) []agent.Option {
				return []agent.Option{
					agent.BeforeCall(func(_ context.Context, _ agent.State, r agent.Request) (agent.Request, error) {
						return r, errors.New("boom")
					}),
				}
			},
			check: func(t *testing.T, p *script, _ *agent.Agent, _ *trace, err error) {
				assert.ErrorContains(t, err, "boom")
				assert.Empty(t, p.received())
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var tr trace
			p := &script{responses: tt.responses}
			opts := append([]agent.Option{agent.Toolset(echo)}, tt.hooks(&tr)...)
			a := agent.New(model(p), opts...)

			events, _, err := collect(t, a.Run(context.Background(), ai.UserMessage("hi")))
			emittest.CheckEvents(t, events)
			tt.check(t, p, a, &tr, err)
		})
	}
}

func TestAfterCall(t *testing.T) {
	tests := []struct {
		name      string
		responses []ai.Message
		hooks     func(tr *trace) []agent.Option
		check     func(t *testing.T, p *script, a *agent.Agent, tr *trace, events []agent.Event, err error)
	}{
		{
			name:      "the chain changes the message in the order of registration",
			responses: []ai.Message{text("ok")},
			hooks: func(tr *trace) []agent.Option {
				return []agent.Option{
					agent.AfterCall(func(_ context.Context, _ agent.State, _ agent.Request, m ai.Message) (ai.Message, error) {
						tr.add("first " + m.Text())
						m.Content = append(m.Content, ai.Text{Text: " one"})
						return m, nil
					}),
					agent.AfterCall(func(_ context.Context, _ agent.State, _ agent.Request, m ai.Message) (ai.Message, error) {
						tr.add("second " + m.Text())
						m.Content = append(m.Content, ai.Text{Text: " two"})
						return m, nil
					}),
				}
			},
			check: func(t *testing.T, _ *script, a *agent.Agent, tr *trace, events []agent.Event, err error) {
				require.NoError(t, err)
				assert.Equal(t, []string{"first ok", "second ok one"}, tr.get())
				assert.Equal(t, []string{"hi", "ok one two"}, historyTexts(a), "the history keeps the returned message")
				end := as[agent.EventMessageEnd](t, events[len(events)-3])
				assert.Equal(t, "ok one two", end.Message.Text(), "message_end has the returned message")
			},
		},
		{
			name:      "a hook reads the request",
			responses: []ai.Message{text("ok")},
			hooks: func(tr *trace) []agent.Option {
				return []agent.Option{
					agent.AfterCall(func(_ context.Context, _ agent.State, r agent.Request, m ai.Message) (ai.Message, error) {
						tr.add(fmt.Sprint(msgTexts(r.Messages)))
						return m, nil
					}),
				}
			},
			check: func(t *testing.T, _ *script, _ *agent.Agent, tr *trace, _ []agent.Event, err error) {
				require.NoError(t, err)
				assert.Equal(t, []string{"[hi]"}, tr.get())
			},
		},
		{
			name: "the loop runs the tool calls of the returned message",
			responses: []ai.Message{
				toolCall("c1", "echo", `{"s":"x"}`),
				text("done"),
			},
			hooks: func(tr *trace) []agent.Option {
				return []agent.Option{
					agent.AfterCall(func(_ context.Context, _ agent.State, _ agent.Request, m ai.Message) (ai.Message, error) {
						if len(m.ToolCalls()) == 0 {
							return m, nil
						}
						return text("no tools"), nil
					}),
				}
			},
			check: func(t *testing.T, p *script, a *agent.Agent, _ *trace, _ []agent.Event, err error) {
				require.NoError(t, err)
				assert.Len(t, p.received(), 1)
				assert.Equal(t, []string{"hi", "no tools"}, historyTexts(a))
			},
		},
		{
			name:      "an error fails the run, and the history does not keep the message",
			responses: []ai.Message{text("ok")},
			hooks: func(tr *trace) []agent.Option {
				return []agent.Option{
					agent.AfterCall(func(_ context.Context, _ agent.State, _ agent.Request, m ai.Message) (ai.Message, error) {
						return m, errors.New("boom")
					}),
				}
			},
			check: func(t *testing.T, _ *script, a *agent.Agent, _ *trace, events []agent.Event, err error) {
				assert.ErrorContains(t, err, "boom")
				assert.Equal(t, []string{"hi"}, historyTexts(a))
				end := as[agent.EventAgentEnd](t, events[len(events)-1])
				assert.Equal(t, ai.Usage{Input: 1, Output: 1}, end.Usage, "the run counts a call that it did not keep")
				assert.Equal(t, ai.Usage{Input: 1, Output: 1}, a.State().Usage())
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var tr trace
			p := &script{responses: tt.responses}
			opts := append([]agent.Option{agent.Toolset(echo)}, tt.hooks(&tr)...)
			a := agent.New(model(p), opts...)

			events, _, err := collect(t, a.Run(context.Background(), ai.UserMessage("hi")))
			emittest.CheckEvents(t, events)
			tt.check(t, p, a, &tr, events, err)
		})
	}
}

func TestBeforeTool(t *testing.T) {
	upper := tool.New("upper", func(_ context.Context, in echoInput) (string, error) {
		return "upper:" + in.S, nil
	}, tool.Description("upper"))

	tests := []struct {
		name    string
		call    ai.Message
		hooks   func(tr *trace) []agent.Option
		results []string
		isError bool
		steps   []string
	}{
		{
			name: "the chain runs in the order of registration",
			call: toolCall("c1", "echo", `{"s":"x"}`),
			hooks: func(tr *trace) []agent.Option {
				return []agent.Option{
					agent.BeforeTool(func(_ context.Context, _ agent.State, c ai.ToolCall) (ai.ToolCall, error) {
						tr.add("first " + c.Name)
						return c, nil
					}),
					agent.BeforeTool(func(_ context.Context, _ agent.State, c ai.ToolCall) (ai.ToolCall, error) {
						tr.add("second " + c.Name)
						return c, nil
					}),
				}
			},
			results: []string{"echo:x"},
			steps:   []string{"first echo", "second echo"},
		},
		{
			name: "Deny blocks the call",
			call: toolCall("c1", "echo", `{"s":"x"}`),
			hooks: func(tr *trace) []agent.Option {
				return []agent.Option{
					agent.BeforeTool(func(_ context.Context, _ agent.State, c ai.ToolCall) (ai.ToolCall, error) {
						return c, agent.Deny("not here")
					}),
					agent.BeforeTool(func(_ context.Context, _ agent.State, c ai.ToolCall) (ai.ToolCall, error) {
						tr.add("second")
						return c, nil
					}),
				}
			},
			results: []string{"not here"},
			isError: true,
		},
		{
			name: "a hook changes the input",
			call: toolCall("c1", "echo", `{"s":"x"}`),
			hooks: func(tr *trace) []agent.Option {
				return []agent.Option{
					agent.BeforeTool(func(_ context.Context, _ agent.State, c ai.ToolCall) (ai.ToolCall, error) {
						c.Input = json.RawMessage(`{"s":"y"}`)
						return c, nil
					}),
				}
			},
			results: []string{"echo:y"},
		},
		{
			name: "a hook changes the tool",
			call: toolCall("c1", "echo", `{"s":"x"}`),
			hooks: func(tr *trace) []agent.Option {
				return []agent.Option{
					agent.BeforeTool(func(_ context.Context, _ agent.State, c ai.ToolCall) (ai.ToolCall, error) {
						c.Name = "upper"
						return c, nil
					}),
				}
			},
			results: []string{"upper:x"},
		},
		{
			name: "an unknown tool runs no hook",
			call: toolCall("c1", "nope", `{}`),
			hooks: func(tr *trace) []agent.Option {
				return []agent.Option{
					agent.BeforeTool(func(_ context.Context, _ agent.State, c ai.ToolCall) (ai.ToolCall, error) {
						tr.add("before")
						return c, nil
					}),
					agent.AfterTool(func(_ context.Context, _ agent.State, _ ai.ToolCall, r ai.ToolResult) (ai.ToolResult, error) {
						tr.add("after")
						return r, nil
					}),
				}
			},
			results: []string{`tool "nope" not found`},
			isError: true,
		},
		{
			name: "the history ends with the assistant message",
			call: toolCall("c1", "echo", `{"s":"x"}`),
			hooks: func(tr *trace) []agent.Option {
				return []agent.Option{
					agent.BeforeTool(func(_ context.Context, s agent.State, c ai.ToolCall) (ai.ToolCall, error) {
						msgs := s.Messages()
						tr.add(string(msgs[len(msgs)-1].Role))
						return c, nil
					}),
				}
			},
			results: []string{"echo:x"},
			steps:   []string{"assistant"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var tr trace
			p := &script{responses: []ai.Message{tt.call, text("done")}}
			opts := append([]agent.Option{agent.Toolset(echo, upper)}, tt.hooks(&tr)...)
			a := agent.New(model(p), opts...)

			events, msgs, err := collect(t, a.Run(context.Background(), ai.UserMessage("hi")))
			require.NoError(t, err)
			emittest.CheckEvents(t, events)
			msgs = afterBase(t, msgs)

			assert.Equal(t, tt.results, resultTexts(msgs))
			assert.Equal(t, tt.isError, msgs[1].IsError)
			assert.Equal(t, "c1", msgs[1].ToolCallID, "the result answers the call of the model")
			assert.Equal(t, tt.steps, tr.get())
		})
	}
}

func TestBeforeToolCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &script{responses: []ai.Message{toolCall("c1", "echo", `{"s":"x"}`)}}
	a := agent.New(model(p),
		agent.Toolset(echo),
		agent.BeforeTool(func(ctx context.Context, _ agent.State, c ai.ToolCall) (ai.ToolCall, error) {
			cancel()
			return c, ctx.Err()
		}),
	)

	events, _, err := collect(t, a.Run(ctx, ai.UserMessage("hi")))
	assert.ErrorIs(t, err, context.Canceled)
	emittest.CheckEvents(t, events)
	assert.Equal(t, []string{"hi", ""}, historyTexts(a), "the result is not added")
}

func TestBeforeToolStopsAfterCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var tr trace
	p := &script{responses: []ai.Message{toolCalls("echo", "echo")}}
	a := agent.New(model(p),
		agent.Toolset(echo),
		agent.BeforeTool(func(_ context.Context, _ agent.State, c ai.ToolCall) (ai.ToolCall, error) {
			tr.add(c.ID)
			cancel()
			return c, nil
		}),
	)

	events, _, err := collect(t, a.Run(ctx, ai.UserMessage("hi")))
	assert.ErrorIs(t, err, context.Canceled)
	emittest.CheckEvents(t, events)
	assert.Equal(t, []string{"c1"}, tr.get(), "no hook runs after the cancel")
}

func TestAfterTool(t *testing.T) {
	tests := []struct {
		name    string
		hooks   func(tr *trace) []agent.Option
		result  string
		isError bool
		steps   []string
	}{
		{
			name: "the chain runs in the order of registration",
			hooks: func(tr *trace) []agent.Option {
				return []agent.Option{
					agent.AfterTool(func(_ context.Context, _ agent.State, _ ai.ToolCall, r ai.ToolResult) (ai.ToolResult, error) {
						tr.add("first " + r.Content[0].(ai.Text).Text)
						return r, nil
					}),
					agent.AfterTool(func(_ context.Context, _ agent.State, c ai.ToolCall, r ai.ToolResult) (ai.ToolResult, error) {
						tr.add("second " + c.Name)
						return r, nil
					}),
				}
			},
			result: "echo:x",
			steps:  []string{"first echo:x", "second echo"},
		},
		{
			name: "a hook changes the result",
			hooks: func(tr *trace) []agent.Option {
				return []agent.Option{
					agent.AfterTool(func(_ context.Context, _ agent.State, _ ai.ToolCall, r ai.ToolResult) (ai.ToolResult, error) {
						r.Content = append(r.Content, ai.Text{Text: " remember"})
						return r, nil
					}),
				}
			},
			result: "echo:x remember",
		},
		{
			name: "an error is the error result",
			hooks: func(tr *trace) []agent.Option {
				return []agent.Option{
					agent.AfterTool(func(_ context.Context, _ agent.State, _ ai.ToolCall, r ai.ToolResult) (ai.ToolResult, error) {
						return r, errors.New("too long")
					}),
					agent.AfterTool(func(_ context.Context, _ agent.State, _ ai.ToolCall, r ai.ToolResult) (ai.ToolResult, error) {
						tr.add("second")
						return r, nil
					}),
				}
			},
			result:  "too long",
			isError: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var tr trace
			p := &script{responses: []ai.Message{
				toolCall("c1", "echo", `{"s":"x"}`),
				text("done"),
			}}
			opts := append([]agent.Option{agent.Toolset(echo)}, tt.hooks(&tr)...)
			a := agent.New(model(p), opts...)

			events, msgs, err := collect(t, a.Run(context.Background(), ai.UserMessage("hi")))
			require.NoError(t, err)
			emittest.CheckEvents(t, events)
			msgs = afterBase(t, msgs)

			assert.Equal(t, tt.result, msgs[1].Text())
			assert.Equal(t, tt.isError, msgs[1].IsError)
			assert.Equal(t, tt.steps, tr.get())

			for _, ev := range events {
				if end, ok := ev.(agent.EventToolExecutionEnd); ok {
					assert.Equal(t, tt.result, ai.ToolResultMessage("", end.Result.Content...).Text(),
						"tool_execution_end has the final result")
				}
			}
		})
	}
}

func TestAfterToolInParallel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	first, second := meeting()
	var tr trace
	p := &script{responses: []ai.Message{
		toolCalls("first", "second"),
		text("done"),
	}}
	a := agent.New(model(p),
		agent.Toolset(first, second),
		agent.AfterTool(func(_ context.Context, _ agent.State, c ai.ToolCall, r ai.ToolResult) (ai.ToolResult, error) {
			tr.add(c.Name)
			r.Content = append(r.Content, ai.Text{Text: "!"})
			return r, nil
		}),
	)

	events, msgs, err := collect(t, a.Run(ctx, ai.UserMessage("hi")))
	require.NoError(t, err, "the calls still run at the same time")
	emittest.CheckEvents(t, events)

	assert.ElementsMatch(t, []string{"first", "second"}, tr.get())
	assert.Equal(t, []string{"first!", "second!"}, resultTexts(msgs))
}

func TestAfterTurn(t *testing.T) {
	tests := []struct {
		name      string
		responses []ai.Message
		hooks     []agent.Option
		calls     int
		history   []string
	}{
		{
			name:      "an append starts no turn",
			responses: []ai.Message{text("ok")},
			hooks: []agent.Option{
				agent.AfterTurn(func(context.Context, agent.State) ([]ai.Message, error) {
					return []ai.Message{ai.UserMessage("note")}, nil
				}),
			},
			calls:   1,
			history: []string{"hi", "ok", "note"},
		},
		{
			name: "the next turn reads the append",
			responses: []ai.Message{
				toolCall("c1", "echo", `{"s":"x"}`),
				text("done"),
			},
			hooks: []agent.Option{
				agent.AfterTurn(func(_ context.Context, s agent.State) ([]ai.Message, error) {
					return []ai.Message{ai.UserMessage(fmt.Sprintf("after %d", s.Turn()))}, nil
				}),
			},
			calls:   2,
			history: []string{"hi", "", "echo:x", "after 1", "done", "after 2"},
		},
		{
			name:      "a hook sees the appends of the hooks before it",
			responses: []ai.Message{text("ok")},
			hooks: []agent.Option{
				agent.AfterTurn(func(context.Context, agent.State) ([]ai.Message, error) {
					return []ai.Message{ai.UserMessage("one")}, nil
				}),
				agent.AfterTurn(func(_ context.Context, s agent.State) ([]ai.Message, error) {
					msgs := s.Messages()
					return []ai.Message{ai.UserMessage("saw " + msgs[len(msgs)-1].Text())}, nil
				}),
			},
			calls:   1,
			history: []string{"hi", "ok", "one", "saw one"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &script{responses: tt.responses}
			opts := append([]agent.Option{agent.Toolset(echo)}, tt.hooks...)
			a := agent.New(model(p), opts...)

			events, _, err := collect(t, a.Run(context.Background(), ai.UserMessage("hi")))
			require.NoError(t, err)
			emittest.CheckEvents(t, events)

			assert.Len(t, p.received(), tt.calls)
			assert.Equal(t, tt.history, historyTexts(a))
		})
	}
}

func TestAfterTurnEvents(t *testing.T) {
	p := &script{responses: []ai.Message{text("ok")}}
	a := agent.New(model(p),
		agent.AfterTurn(func(context.Context, agent.State) ([]ai.Message, error) {
			return []ai.Message{
				ai.UserMessage("note"),
				ai.UserMessage("now"),
			}, nil
		}),
	)

	events, msgs, err := collect(t, a.Run(context.Background(), ai.UserMessage("hi")))
	require.NoError(t, err)
	emittest.CheckEvents(t, events)

	tail := events[len(events)-6:]
	assert.Equal(t, types([]agent.Event{
		agent.EventTurnEnd{},
		agent.EventMessageStart{},
		agent.EventMessageEnd{},
		agent.EventMessageStart{},
		agent.EventMessageEnd{},
		agent.EventAgentEnd{},
	}), types(tail), "the messages come after turn_end")

	assert.Equal(t, "note", as[agent.EventMessageEnd](t, tail[2]).Message.Text())
	assert.Equal(t, "now", as[agent.EventMessageEnd](t, tail[4]).Message.Text())
	assert.Equal(t, []string{"hi", "ok", "note", "now"}, historyTexts(a))
	assert.Equal(t, []string{"ok", "note", "now"}, msgTexts(msgs), "the result has the messages")
}

func TestBeforeStop(t *testing.T) {
	tests := []struct {
		name      string
		responses []ai.Message
		opts      []agent.Option
		calls     int
		history   []string
	}{
		{
			name:      "messages continue the run",
			responses: []ai.Message{text("one"), text("two")},
			opts: []agent.Option{
				agent.BeforeStop(once("go on")),
			},
			calls:   2,
			history: []string{"hi", "one", "go on", "two"},
		},
		{
			name:      "the first hook with messages wins",
			responses: []ai.Message{text("one"), text("two"), text("three")},
			opts: []agent.Option{
				agent.BeforeStop(func(context.Context, agent.State) ([]ai.Message, error) {
					return nil, nil
				}),
				agent.BeforeStop(once("second")),
				agent.BeforeStop(once("third")),
			},
			calls:   3,
			history: []string{"hi", "one", "second", "two", "third", "three"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &script{responses: tt.responses}
			a := agent.New(model(p), tt.opts...)

			events, _, err := collect(t, a.Run(context.Background(), ai.UserMessage("hi")))
			require.NoError(t, err)
			emittest.CheckEvents(t, events)

			assert.Len(t, p.received(), tt.calls)
			assert.Equal(t, tt.history, historyTexts(a))
		})
	}
}

// policy is a hook at two points. It records each point that it sees.
type policy struct {
	agent.BaseHook
	tr *trace
}

func (p policy) BeforeCall(_ context.Context, s agent.State, r agent.Request) (agent.Request, error) {
	p.tr.add(fmt.Sprintf("call %d", s.Turn()))
	return r, nil
}

func (p policy) BeforeTool(_ context.Context, _ agent.State, c ai.ToolCall) (ai.ToolCall, error) {
	p.tr.add("tool " + c.Name)
	return c, nil
}

func TestHooks(t *testing.T) {
	var tr trace
	p := &script{responses: []ai.Message{
		toolCall("c1", "echo", `{"s":"x"}`),
		text("done"),
	}}
	a := agent.New(model(p),
		agent.Toolset(echo),
		agent.BeforeTool(func(_ context.Context, _ agent.State, c ai.ToolCall) (ai.ToolCall, error) {
			tr.add("first")
			return c, nil
		}),
		agent.Hooks(policy{tr: &tr}),
	)

	events, msgs, err := collect(t, a.Run(context.Background(), ai.UserMessage("hi")))
	require.NoError(t, err)
	emittest.CheckEvents(t, events)

	assert.Equal(t, []string{"call 1", "first", "tool echo", "call 2"}, tr.get(),
		"a hook takes its place in each chain in the order of registration")
	assert.Equal(t, []string{"echo:x"}, resultTexts(msgs), "BaseHook keeps the result")
	assert.Equal(t, []string{"hi", "", "echo:x", "done"}, historyTexts(a), "BaseHook adds no message")
}
