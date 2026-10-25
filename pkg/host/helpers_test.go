package host_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/catalog"
	"github.com/sonnes/gopi/pkg/host"
	"github.com/sonnes/gopi/pkg/session"
	"github.com/sonnes/gopi/pkg/tool"
)

// waitTimeout is how long a test waits for an event.
const waitTimeout = 5 * time.Second

// fakeModel answers each call with the next reply, or with "ok". If
// gate is set, each call waits for a value from gate, or for the end of
// its context.
type fakeModel struct {
	mu      sync.Mutex
	replies []ai.Message
	gate    chan struct{}
	calls   [][]ai.Message
	// fail, if set, fails each call after the gate.
	fail error
}

func (f *fakeModel) StreamText(
	ctx context.Context,
	_ ai.Model,
	msgs []ai.Message,
	_ ai.Options,
) *ai.EventStream {
	f.mu.Lock()
	f.calls = append(f.calls, msgs)
	next := reply("ok")
	if len(f.replies) > 0 {
		next = f.replies[0]
		f.replies = f.replies[1:]
	}
	gate := f.gate
	fail := f.fail
	f.mu.Unlock()

	return ai.NewEventStream(func(push func(ai.Event)) (*ai.Message, error) {
		if gate != nil {
			select {
			case <-gate:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if fail != nil {
			return nil, fail
		}
		push(ai.Event{Type: ai.EventStart})
		return &next, nil
	})
}

// texts returns the text of the last message of each call.
func (f *fakeModel) texts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, msgs := range f.calls {
		out = append(out, msgs[len(msgs)-1].Text())
	}
	return out
}

// waitCalls waits until the model got n calls.
func (f *fakeModel) waitCalls(t *testing.T, n int) {
	t.Helper()
	require.Eventually(t, func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return len(f.calls) >= n
	}, waitTimeout, time.Millisecond)
}

// reply returns an assistant message with text.
func reply(text string) ai.Message {
	m := ai.AssistantMessage(ai.Text{Text: text})
	m.StopReason = ai.StopReasonStop
	m.Usage = ai.Usage{
		Input:  10,
		Output: 5,
	}
	return m
}

// fakeDriver starts a loop agent over its model, with the echo tool,
// and records each config. With an approve function in the config, it
// asks before each tool call, as a permission policy does.
type fakeDriver struct {
	lm  ai.LanguageModel
	err error
	// plain hides the optional interfaces of the runner.
	plain bool
	// detached calls the approve function with a context that the run
	// does not end, as the Codex agent does.
	detached bool
	// abandon stops the wait for the approve function when the run
	// ends, as the Codex agent does. The approve function goes on.
	abandon bool
	// reason goes with each call to the approve function, as an agent
	// says why a call asks.
	reason string

	mu      sync.Mutex
	configs []agent.Config
}

func (d *fakeDriver) Start(ctx context.Context, c agent.Config) (agent.Runner, error) {
	d.mu.Lock()
	d.configs = append(d.configs, c)
	d.mu.Unlock()
	if d.err != nil {
		return nil, d.err
	}
	opts := []agent.Option{
		agent.Store(c.Store),
		agent.Toolset(append([]tool.Tool{echo}, c.Tools...)...),
		agent.Hooks(c.Hooks...),
	}
	if c.Approve != nil {
		approve := c.Approve
		if d.detached {
			approve = func(_ context.Context, req agent.ApprovalRequest) (agent.Decision, error) {
				return c.Approve(context.Background(), req)
			}
		}
		if d.abandon {
			approve = abandoned(approve)
		}
		opts = append(opts, agent.BeforeTool(askFirst(approve, d.reason)))
	}
	r, err := agent.Durable(ctx, d.lm, c.Session, opts...)
	if err != nil || !d.plain {
		return r, err
	}
	return plainRunner{Runner: r}, nil
}

// abandoned returns an approve function that calls approve in its own
// goroutine, and returns the error of ctx if ctx ends first.
func abandoned(approve agent.ApproveFunc) agent.ApproveFunc {
	type result struct {
		d   agent.Decision
		err error
	}
	return func(ctx context.Context, req agent.ApprovalRequest) (agent.Decision, error) {
		done := make(chan result, 1)
		go func() {
			d, err := approve(ctx, req)
			done <- result{
				d:   d,
				err: err,
			}
		}()
		select {
		case r := <-done:
			return r.d, r.err
		case <-ctx.Done():
			return agent.Decision{}, ctx.Err()
		}
	}
}

// askFirst returns a hook that asks approve before each tool call, with
// reason.
func askFirst(approve agent.ApproveFunc, reason string) agent.BeforeToolFunc {
	return func(ctx context.Context, _ agent.State, c ai.ToolCall) (ai.ToolCall, error) {
		req := agent.ApprovalRequest{
			Call:   c,
			Reason: reason,
		}
		d, err := approve(ctx, req)
		if err != nil {
			return c, err
		}
		if !d.Allow {
			return c, agent.Deny(d.Message)
		}
		if d.Input != nil {
			c.Input = d.Input
		}
		return c, nil
	}
}

type echoInput struct {
	S string `json:"s"`
}

// echo returns its input.
var echo = tool.New("echo", func(_ context.Context, in echoInput) (string, error) {
	return in.S, nil
})

// callTool returns an assistant message that calls the tool name.
func callTool(id, name, input string) ai.Message {
	m := ai.AssistantMessage(ai.ToolCall{
		ID:    id,
		Name:  name,
		Input: json.RawMessage(input),
	})
	m.StopReason = ai.StopReasonToolUse
	return m
}

// toolResults returns the text of each tool result of s.
func toolResults(s *host.Session) []string {
	var out []string
	for _, m := range s.Log().Messages() {
		if m.Role == ai.RoleToolResult {
			out = append(out, m.Text())
		}
	}
	return out
}

// fixture is a host on the fake model "fake/m".
type fixture struct {
	h      *host.Host
	model  *fakeModel
	driver *fakeDriver
	store  *session.MemoryStore
}

func newFixture(t *testing.T, opts ...host.Option) fixture {
	t.Helper()
	f := fixture{
		model: &fakeModel{},
		store: session.NewMemoryStore(),
	}
	cat := catalog.New()
	cat.RegisterTextProvider("fake", f.model, ai.Model{ID: "m"})
	lm, err := cat.LanguageModel("fake/m")
	require.NoError(t, err)
	f.driver = &fakeDriver{lm: lm}
	cat.DefaultDriver(f.driver)

	opts = append([]host.Option{host.Store(f.store)}, opts...)
	f.h = host.New(cat, opts...)
	t.Cleanup(func() { require.NoError(t, f.h.Close()) })
	return f
}

// start starts a session on the fake model.
func (f fixture) start(t *testing.T, spec host.Spec) *host.Session {
	t.Helper()
	if spec.Model == "" {
		spec.Model = "fake/m"
	}
	s, err := f.h.Start(context.Background(), spec)
	require.NoError(t, err)
	return s
}

// watch follows the host stream of f until the test ends. It returns
// the state at the start, and a channel of the events.
func (f fixture) watch(t *testing.T) (host.State, <-chan host.Event) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	state, events := f.h.Events(ctx)
	ch := make(chan host.Event, 100)
	go func() {
		defer close(ch)
		for e, err := range events {
			if err != nil {
				return
			}
			ch <- e
		}
	}()
	return state, ch
}

// next returns the next event of ch that matches. It fails the test if
// no such event comes.
func next[E any](t *testing.T, ch <-chan E, match func(E) bool) E {
	t.Helper()
	timeout := time.After(waitTimeout)
	for {
		select {
		case e, ok := <-ch:
			require.True(t, ok, "the stream ended")
			if match(e) {
				return e
			}
		case <-timeout:
			require.FailNow(t, "no matching event")
		}
	}
}

// is matches an event of the type T.
func is[T any, E any](e E) bool {
	_, ok := any(e).(T)
	return ok
}

// drain reads ch until it closes, and returns its events.
func drain[E any](t *testing.T, ch <-chan E) []E {
	t.Helper()
	var out []E
	timeout := time.After(waitTimeout)
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, e)
		case <-timeout:
			require.FailNow(t, "the stream did not end")
		}
	}
}
