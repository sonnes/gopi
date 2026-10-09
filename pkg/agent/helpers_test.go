package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/tool"
)

// script is a [ai.TextProvider] that answers each call with the next
// scripted message. It records what it received.
type script struct {
	mu        sync.Mutex
	responses []ai.Message
	calls     [][]ai.Message
	opts      []ai.Options
	// gate, if set, blocks each call until it is closed.
	gate chan struct{}
	// fail, if set, makes every call fail in mid-stream.
	fail error
	// failWithToolStart, with fail, starts a tool call before the
	// failure.
	failWithToolStart bool
}

func (p *script) StreamText(
	ctx context.Context,
	_ ai.Model,
	messages []ai.Message,
	opts ai.Options,
) *ai.EventStream {
	p.mu.Lock()
	p.calls = append(p.calls, messages)
	p.opts = append(p.opts, opts)
	var next *ai.Message
	if len(p.responses) > 0 {
		m := p.responses[0]
		p.responses = p.responses[1:]
		next = &m
	}
	gate := p.gate
	fail := p.fail
	failWithToolStart := p.failWithToolStart
	p.mu.Unlock()

	return ai.NewEventStream(func(push func(ai.Event)) (*ai.Message, error) {
		if gate != nil {
			select {
			case <-gate:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		push(ai.Event{Type: ai.EventStart})
		if fail != nil {
			push(ai.Event{Type: ai.EventTextDelta, Delta: "par"})
			if failWithToolStart {
				push(ai.Event{Type: ai.EventToolStart, ContentIndex: 1})
			}
			return nil, fail
		}
		if next == nil {
			return nil, errors.New("script: no response left")
		}
		for i, c := range next.Content {
			switch b := c.(type) {
			case ai.Text:
				push(ai.Event{Type: ai.EventTextDelta, ContentIndex: i, Delta: b.Text})
			case ai.ToolCall:
				call := b
				push(ai.Event{Type: ai.EventToolEnd, ContentIndex: i, ToolCall: &call})
			}
		}
		return next, nil
	})
}

func (p *script) received() [][]ai.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func text(s string) ai.Message {
	m := ai.AssistantMessage(ai.Text{Text: s})
	m.StopReason = ai.StopReasonStop
	m.Usage = ai.Usage{Input: 1, Output: 1}
	return m
}

func toolCall(id, name, input string) ai.Message {
	m := ai.AssistantMessage(ai.ToolCall{ID: id, Name: name, Input: json.RawMessage(input)})
	m.StopReason = ai.StopReasonToolUse
	m.Usage = ai.Usage{Input: 1, Output: 1}
	return m
}

func model(p *script) ai.LanguageModel {
	return ai.LanguageModel{Model: ai.Model{ID: "test"}, Provider: p}
}

func collect(t *testing.T, s *agent.Stream) ([]agent.Event, []ai.Message, error) {
	t.Helper()
	var events []agent.Event
	for evt, err := range s.Events() {
		if err != nil {
			msgs, werr := s.Wait()
			return events, msgs, werr
		}
		events = append(events, evt)
	}
	msgs, err := s.Wait()
	return events, msgs, err
}

// types returns the type name of each event.
func types(events []agent.Event) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, fmt.Sprintf("%T", e))
	}
	return out
}

// as returns e as a T, and fails t if e is another type.
func as[T agent.Event](t *testing.T, e agent.Event) T {
	t.Helper()
	v, ok := e.(T)
	require.Truef(t, ok, "event is %T", e)
	return v
}

// history returns the messages that the model of a reads next.
func history(a *agent.Agent) []ai.Message {
	return a.State().Messages()
}

// afterBase returns msgs without the base, the system message that the
// first run of an agent with tools adds first.
func afterBase(t *testing.T, msgs []ai.Message) []ai.Message {
	t.Helper()
	require.NotEmpty(t, msgs)
	require.Equal(t, ai.RoleSystem, msgs[0].Role, "the run adds the base first")
	return msgs[1:]
}

// historyTexts returns the texts of the history of a, after the base if
// the history has one.
func historyTexts(a *agent.Agent) []string {
	msgs := history(a)
	if len(msgs) > 0 && msgs[0].Role == ai.RoleSystem {
		msgs = msgs[1:]
	}
	return msgTexts(msgs)
}

func msgTexts(msgs []ai.Message) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m.Text())
	}
	return out
}

// testTimeout ends a test that waits on two tools that never meet.
const testTimeout = 5 * time.Second

// toolCalls returns an assistant message that calls each tool in names,
// with the IDs c1, c2, and so on.
func toolCalls(names ...string) ai.Message {
	var content []ai.Content
	for i, name := range names {
		content = append(content, ai.ToolCall{
			ID:    "c" + string(rune('1'+i)),
			Name:  name,
			Input: json.RawMessage(`{}`),
		})
	}
	m := ai.AssistantMessage(content...)
	m.StopReason = ai.StopReasonToolUse
	return m
}

func textResult(s string) ai.ToolResult {
	return ai.ToolResult{Content: []ai.Content{ai.Text{Text: s}}}
}

type echoInput struct {
	S string `json:"s"`
}

var echo = tool.New("echo", func(_ context.Context, in echoInput) (string, error) {
	return "echo:" + in.S, nil
}, tool.Description("echoes"))

var upper = tool.New("upper", func(_ context.Context, in echoInput) (string, error) {
	return strings.ToUpper(in.S), nil
}, tool.Description("upper-cases"))

type cityInput struct {
	City string `json:"city" jsonschema:"The name of the city"`
}

var weather = tool.New("get_weather",
	func(_ context.Context, in cityInput) (string, error) {
		return "Sunny in " + in.City, nil
	},
	tool.Description("Get the weather forecast."),
	tool.Deferred(),
)

// meeting returns two tools. first waits until second has run, so the
// two finish only if they run at the same time.
func meeting() (first, second tool.Tool) {
	met := make(chan struct{})
	first = tool.Func("first", func(ctx context.Context, _ ai.ToolCall) (ai.ToolResult, error) {
		select {
		case <-met:
			return textResult("first"), nil
		case <-ctx.Done():
			return ai.ToolResult{}, ctx.Err()
		}
	})
	second = tool.Func("second", func(context.Context, ai.ToolCall) (ai.ToolResult, error) {
		close(met)
		return textResult("second"), nil
	})
	return first, second
}

var (
	rules    = ai.Section{Name: "rules", Text: "<rules>Be brief.</rules>"}
	thorough = ai.Section{Name: "rules", Text: "<rules>Be thorough.</rules>"}
	todos    = ai.Section{Name: "todos", Text: "<todos>- fix the test</todos>"}
)

// sequence returns a sections function that gives the next list of
// steps on each call, and the last list after that.
func sequence(steps ...[]ai.Section) func(context.Context) ([]ai.Section, error) {
	var mu sync.Mutex
	i := 0
	return func(context.Context) ([]ai.Section, error) {
		mu.Lock()
		defer mu.Unlock()
		out := steps[min(i, len(steps)-1)]
		i++
		return out, nil
	}
}

// systemPrompt returns the system prompt of opts.
func systemPrompt(opts ai.Options) []ai.Content {
	system, _ := ai.GetOption(opts, ai.SystemPrompt)
	return system
}

// names returns the names of the tools.
func names(tools []ai.ToolInfo) []string {
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		out = append(out, t.Name)
	}
	return out
}

// toolNames returns the names of the tools of opts.
func toolNames(opts ai.Options) []string {
	tools, _ := ai.GetOption(opts, ai.Tools)
	return names(tools)
}

// once returns a BeforeStop hook that continues the run one time with
// the text s.
func once(s string) agent.BeforeStopFunc {
	done := false
	return func(context.Context, agent.State) ([]ai.Message, error) {
		if done {
			return nil, nil
		}
		done = true
		return []ai.Message{ai.UserMessage(s)}, nil
	}
}
