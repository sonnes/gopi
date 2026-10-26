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
// and records each config. With ask, it asks the approve function of
// the config before each tool call, as a permission policy does.
type fakeDriver struct {
	lm  ai.LanguageModel
	err error
	// ask asks before each tool call.
	ask bool
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
	err := d.err
	d.mu.Unlock()
	if err != nil {
		return nil, err
	}
	opts := []agent.Option{
		agent.Store(c.Store),
		agent.Toolset(append([]tool.Tool{echo}, c.Tools...)...),
		agent.Hooks(c.Hooks...),
	}
	if d.ask {
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

// setErr sets the error of each later start.
func (d *fakeDriver) setErr(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.err = err
}

// starts returns the number of starts of the driver.
func (d *fakeDriver) starts() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.configs)
}

// lastConfig returns the config of the last start of the driver.
func (f fixture) lastConfig(t *testing.T) agent.Config {
	t.Helper()
	f.driver.mu.Lock()
	defer f.driver.mu.Unlock()
	require.NotEmpty(t, f.driver.configs)
	return f.driver.configs[len(f.driver.configs)-1]
}

// toolResults returns the text of each tool result of the session id.
func (f fixture) toolResults(t *testing.T, id string) []string {
	t.Helper()
	log, err := f.h.Log(context.Background(), id)
	require.NoError(t, err)
	var out []string
	for _, m := range log.Messages() {
		if m.Role == ai.RoleToolResult {
			out = append(out, m.Text())
		}
	}
	return out
}

// fixture is a host on the fake model "fake/m".
type fixture struct {
	h      *host.Host
	cat    *catalog.Catalog
	model  *fakeModel
	driver *fakeDriver
	store  *session.MemoryStore
}

func newFixture(t *testing.T, opts ...host.Option) fixture {
	t.Helper()
	f := fixture{
		model: &fakeModel{},
		store: session.NewMemoryStore(),
		cat:   catalog.New(),
	}
	f.cat.RegisterTextProvider("fake", f.model, ai.Model{ID: "m"})
	lm, err := f.cat.LanguageModel("fake/m")
	require.NoError(t, err)
	f.driver = &fakeDriver{lm: lm}
	f.cat.DefaultDriver(f.driver)
	f.h = f.open(t, opts...)
	return f
}

// open opens a host on the store, the catalog, and the driver of f.
func (f fixture) open(t *testing.T, opts ...host.Option) *host.Host {
	t.Helper()
	opts = append([]host.Option{host.Store(f.store)}, opts...)
	h, err := host.Open(context.Background(), f.cat, opts...)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, h.Close()) })
	return h
}

// restart closes the host of f, and returns f with a new host on the
// same store, as after a restart of the program.
func (f fixture) restart(t *testing.T) fixture {
	t.Helper()
	require.NoError(t, f.h.Close())
	f.h = f.open(t)
	return f
}

// create creates a session on the fake model.
func (f fixture) create(t *testing.T, config host.Config) host.Session {
	t.Helper()
	if config.Model == "" {
		config.Model = "fake/m"
	}
	sess, err := f.h.Create(context.Background(), config)
	require.NoError(t, err)
	return sess
}

// send sends text to the session id, and fails the test on an error.
func (f fixture) send(t *testing.T, id, text string) {
	t.Helper()
	require.NoError(t, f.h.Send(context.Background(), id, host.Text(text)))
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

// follow reads the agent stream of the session id until the test ends.
// It returns the state at the start, and a channel of the events.
func (f fixture) follow(t *testing.T, id string) (agent.State, <-chan agent.Event) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	state, events, err := f.h.Stream(ctx, id)
	require.NoError(t, err)
	ch := make(chan agent.Event, 100)
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

// gated returns a fixture whose model waits on a gate. close(gate) lets
// each call go on.
func gated(t *testing.T) (fixture, chan struct{}) {
	t.Helper()
	f := newFixture(t)
	gate := make(chan struct{})
	f.model.gate = gate
	return f, gate
}

// status matches an event of a session with the status st.
func status(st host.Status) func(host.Event) bool {
	return func(e host.Event) bool { return e.Session.Status == st }
}

// runEnd returns the session at the next end of a run on events.
func runEnd(t *testing.T, events <-chan host.Event) host.Session {
	t.Helper()
	e := next(t, events, func(e host.Event) bool {
		return e.Session.Status == host.StatusIdle && e.Session.StopReason != ""
	})
	return e.Session
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

// plainRunner hides the optional interfaces of its runner, such as
// [agent.Steerer], as a CLI agent does.
type plainRunner struct {
	agent.Runner
}
