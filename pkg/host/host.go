package host

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"iter"
	"sync"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/catalog"
	"github.com/sonnes/gopi/pkg/session"
	"github.com/sonnes/gopi/pkg/tool"
)

// The errors of a host. A caller tests for them with [errors.Is].
var (
	// ErrExists reports a session ID that the host runs.
	ErrExists = errors.New("host: the session exists")
	// ErrClosed reports a call to a host or a session that closed.
	ErrClosed = errors.New("host: closed")
	// ErrBusy reports a message with [Reject] to a busy session.
	ErrBusy = errors.New("host: the session is busy")
	// ErrInvalid reports a message that the host cannot take.
	ErrInvalid = errors.New("host: invalid input")
)

// Host runs sessions of gopi agents. Create one with [New]. A Host is
// safe for concurrent use.
type Host struct {
	cat   *catalog.Catalog
	store session.Storer
	hooks []agent.Hook
	// hub has the state of each session, and sends each host event.
	hub *hub[State, Event]

	mu sync.Mutex
	// sessions are the sessions that run. A nil value is an ID that a
	// Start holds.
	sessions map[string]*Session
	closed   bool
}

// Option configures a [Host].
type Option func(*Host)

// Store sets the store of the sessions. Without it, the sessions live
// in memory and end with the process.
func Store(s session.Storer) Option {
	return func(h *Host) { h.store = s }
}

// Hooks adds hooks to each session. A driver that cannot call a point
// of a hook fails the start of the session.
func Hooks(hs ...agent.Hook) Option {
	return func(h *Host) { h.hooks = append(h.hooks, hs...) }
}

// New returns a host that resolves the model spec of each session with
// cat. The catalog must have a driver for each spec, or a default
// driver.
func New(cat *catalog.Catalog, opts ...Option) *Host {
	h := &Host{
		cat:      cat,
		sessions: make(map[string]*Session),
	}
	for _, opt := range opts {
		opt(h)
	}
	if h.store == nil {
		h.store = session.NewMemoryStore()
	}
	h.hub = newHub(
		State{},
		(*State).Apply,
		State.Clone,
	)
	return h
}

// Spec defines the agent of a session. A zero field keeps the default
// of the driver.
type Spec struct {
	// ID is the ID of the session. If it is empty, Start makes a new
	// ID. If the store has a log with the ID, the session resumes it.
	ID string
	// Dirs are folders with agents/, skills/, and AGENTS.md, lowest
	// first. A later folder wins for a name. The host reads only the
	// file of Agent. The driver gets Dirs.
	Dirs []string
	// Agent names an agent file in Dirs. The file fills each empty field
	// of Model, Mode, and Effort. Its prompt goes before System, its
	// disallowed tools go after Deny, and its skills go after Skills. The
	// host does not read the tools and the maxTurns keys of the file. A
	// resumed session runs the agent that it started with, as
	// [SessionAgent] keeps it.
	Agent string
	// Model is the model spec, such as "claude-code/claude-opus-5-5".
	// It picks the driver. It is required if Agent gives no model.
	Model string
	// Effort is the thinking level of each model call. If it is empty,
	// the driver keeps the default of the model. A value that is not a
	// level fails the start.
	Effort ai.ThinkingLevel
	// Mode is the permission mode of the agent. If it is empty, the
	// driver keeps its own mode. A driver that cannot honor the mode
	// fails the start.
	Mode agent.Mode
	// Dir is the working directory of the agent.
	Dir string
	// System is the text that the agent adds to its own prompt.
	System string
	// Skills names the skills that the agent loads at the start.
	Skills []string
	// Allow, Ask, and Deny are the permission rules of the agent, in
	// the format "Tool" or "Tool(content)".
	Allow []string
	Ask   []string
	Deny  []string
	// Approve decides each tool call that needs a person. If it is nil,
	// the agent denies each such call. Its context ends when the run
	// ends. It must not call Close on the session or the host, because
	// Close waits for the run, and the run waits for Approve.
	Approve agent.ApproveFunc
	// Tools are more tools for the agent. Only the harness takes them.
	Tools []tool.Tool
}

// Start starts the agent of spec and returns its session.
//
// If the host runs a session with the ID, Start returns [ErrExists].
// If the host closed, Start returns [ErrClosed]. If the driver fails,
// Start returns its error, and the host keeps no session.
func (h *Host) Start(ctx context.Context, spec Spec) (*Session, error) {
	id := spec.ID
	if id == "" {
		id = newSessionID()
	}
	if err := h.reserve(id); err != nil {
		return nil, err
	}

	s, resumed, err := h.startSession(ctx, id, spec)
	if err != nil {
		h.release(id)
		return nil, err
	}

	go s.work()

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil, errors.Join(ErrClosed, s.stop())
	}
	// The host publishes the start under its lock, so that no event of
	// the session reaches the stream before it.
	h.sessions[id] = s
	// The spec of the session has the fields of its agent file.
	h.hub.publish(EventSessionStart{
		Session: id,
		Model:   s.spec.Model,
		Mode:    s.spec.Mode,
		Dir:     s.spec.Dir,
		Resumed: resumed,
	})
	h.mu.Unlock()
	return s, nil
}

// startSession starts the agent of the session id. It reports if the
// store had the log of the session.
func (h *Host) startSession(ctx context.Context, id string, spec Spec) (*Session, bool, error) {
	store := h.store
	snap, err := h.store.Load(ctx, id)
	resumed := err == nil
	if err != nil && !errors.Is(err, session.ErrNotFound) {
		return nil, false, fmt.Errorf("host: session %s: %w", id, err)
	}
	if resumed {
		// The driver gets the snapshot, so it does not read the log a
		// second time.
		store = session.Preloaded(h.store, id, snap)
	}
	spec, pin, err := withAgent(ctx, spec, snap, resumed)
	if err == nil {
		err = checkEffort(spec)
	}
	if err != nil {
		return nil, false, fmt.Errorf("host: session %s: %w", id, err)
	}
	d, model, err := h.cat.Driver(spec.Model)
	if err != nil {
		return nil, false, fmt.Errorf("host: session %s: %w", id, err)
	}
	if pin != nil {
		// The session keeps its agent before the driver starts, so a
		// crash in the start cannot leave a session with no agent. The
		// driver then reads the session from the store.
		if err := keepAgent(ctx, h.store, id, *pin); err != nil {
			return nil, false, fmt.Errorf("host: session %s: keep the agent: %w", id, err)
		}
		store = h.store
	}

	// The session exists before its agent, so that the approve function
	// of the agent can reach it.
	s := newSession(h, id, spec)
	runner, err := d.Start(ctx, agent.Config{
		Session: id,
		Store:   store,
		Spec:    spec.Model,
		Model:   model,
		Mode:    spec.Mode,
		Dir:     spec.Dir,
		System:  spec.System,
		Skills:  spec.Skills,
		Dirs:    spec.Dirs,
		Effort:  spec.Effort,
		Allow:   spec.Allow,
		Ask:     spec.Ask,
		Deny:    spec.Deny,
		Approve: s.approveFunc(),
		Hooks:   h.hooks,
		Tools:   spec.Tools,
	})
	if err != nil {
		err = fmt.Errorf("host: start session %s: %w", id, err)
		s.cancel(err)
		return nil, false, err
	}
	if err := s.setRunner(runner); err != nil {
		s.cancel(err)
		return nil, false, errors.Join(err, closeRunner(runner))
	}
	return s, resumed, nil
}

// reserve holds id for a session that starts.
func (h *Host) reserve(id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return ErrClosed
	}
	if _, taken := h.sessions[id]; taken {
		return fmt.Errorf("host: session %s: %w", id, ErrExists)
	}
	h.sessions[id] = nil
	return nil
}

func (h *Host) release(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.sessions, id)
}

// end publishes the end of the session id, and frees its ID. It does
// both under the lock of the host, so that a new session with the ID
// starts after the end on the stream.
func (h *Host) end(e EventSessionEnd) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.hub.publish(e)
	delete(h.sessions, e.Session)
}

// Session returns the session id, if the host runs it.
func (h *Host) Session(id string) (*Session, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.sessions[id]
	return s, s != nil
}

// Events returns the state of each session of the host, and the stream
// of the host events after that state. A reader applies each event to
// the state with [State.Apply].
//
// The stream ends when the host closes, with the error of ctx when ctx
// ends, or with [ErrSlowReader]. After ErrSlowReader, call Events
// again.
func (h *Host) Events(ctx context.Context) (State, iter.Seq2[Event, error]) {
	return h.hub.subscribe(ctx)
}

// Close closes each session, and then ends each stream of the host. A
// second Close does nothing. Do not call Close from an approve function
// or a hook, because Close waits for each run.
func (h *Host) Close() error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}
	h.closed = true
	var sessions []*Session
	for _, s := range h.sessions {
		if s != nil {
			sessions = append(sessions, s)
		}
	}
	h.mu.Unlock()

	var errs []error
	for _, s := range sessions {
		errs = append(errs, s.Close())
	}
	h.hub.close()
	return errors.Join(errs...)
}

// sessionIDBytes is the number of random bytes in a new session ID.
const sessionIDBytes = 8

func newSessionID() string {
	b := make([]byte, sessionIDBytes)
	_, _ = rand.Read(b)
	return "s_" + hex.EncodeToString(b)
}
