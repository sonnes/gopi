package host

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"iter"
	"maps"
	"slices"
	"sync"

	"github.com/google/uuid"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/catalog"
	"github.com/sonnes/gopi/pkg/def"
	"github.com/sonnes/gopi/pkg/session"
	"github.com/sonnes/gopi/pkg/tool"
)

// The errors of a host. A caller tests for them with [errors.Is].
var (
	// ErrExists reports a session ID that the store has.
	ErrExists = errors.New("host: the session exists")
	// ErrNotFound reports a session ID that the store does not have, or
	// a tool call that does not wait.
	ErrNotFound = errors.New("host: not found")
	// ErrClosed reports a call to a host that closed.
	ErrClosed = errors.New("host: closed")
	// ErrInvalid reports an input that the host cannot take.
	ErrInvalid = errors.New("host: invalid input")
	// ErrBusy reports that the session has an active run.
	ErrBusy = errors.New("host: the session has an active run")
	// ErrChild reports a message, an update, or an interrupt for the
	// session of a subagent. The run of its parent writes it.
	ErrChild = errors.New("host: the session is a subagent")
)

// Storer keeps the sessions of a host. The host lists the store when it
// opens, so it knows each session, also a session with no agent. The fs
// store of gopi and [session.MemoryStore] satisfy it.
type Storer interface {
	session.Storer
	// List returns each session of the store.
	List(ctx context.Context) ([]session.Snapshot, error)
}

// Host runs sessions of gopi agents. Create one with [Open]. A Host is
// safe for concurrent use.
type Host struct {
	cat   *catalog.Catalog
	store Storer
	hooks []agent.Hook
	// compaction is the strategy of [Compaction], or nil.
	compaction agent.Compactor
	tools      []tool.Tool
	// skillset reads the skills of [Skillset].
	skillset []def.SkillsReader
	// sections returns the sections of a session, by ID.
	sections SectionsFunc
	// toolList reports a call of [Tools], also with no tools.
	toolList bool
	// newID makes the ID of a session that the caller gives no ID.
	newID func() string
	// hub has each session, and sends each change.
	hub *hub[State, Event]

	mu sync.Mutex
	// sessions has each session of the store, by ID.
	sessions map[string]*hosted
	// running has the children that run, by the ID of their parent, so
	// the end of a run finds its children with no scan of the sessions.
	running map[string]map[string]*hosted
	closed  bool
}

// Option configures a [Host].
type Option func(*Host)

// Store sets the store of the sessions. Without it, the sessions live
// in memory and end with the process.
func Store(s Storer) Option {
	return func(h *Host) { h.store = s }
}

// Hooks adds hooks to each session. A driver that cannot call a point
// of a hook fails the start of the agent.
func Hooks(hs ...agent.Hook) Option {
	return func(h *Host) { h.hooks = append(h.hooks, hs...) }
}

// Compaction sets the strategy that compacts the history of each session
// of the harness. Without it, the harness uses its default strategy. A
// driver of a CLI ignores it.
func Compaction(c agent.Compactor) Option {
	return func(h *Host) { h.compaction = c }
}

// Tools adds tools to the list of the host. The list is the full set of
// tools of each session, and each driver gets it: the harness runs the
// tools, and a driver of a CLI maps each well-known name to a tool of
// its CLI. [Config.ToolNames] and the Deny rules pick a subset. [Open]
// needs Tools, so a host never has no tool by mistake. Tools with no
// tools gives each session no tool. A tool is code, so the config
// value of a session cannot keep it. A session that names a tool of an
// earlier list fails at its next start.
func Tools(ts ...tool.Tool) Option {
	return func(h *Host) {
		h.tools = append(h.tools, ts...)
		h.toolList = true
	}
}

// Skillset adds readers of skills to each session, such as skills that
// ship in the program. Each driver gets them. They are lower than
// [Config.Dirs], so a skill of a folder replaces a skill of Skillset with
// the same name. A reader is code, so the config value of a session
// cannot keep it, and a session that started earlier gets the skills at
// its next start.
func Skillset(readers ...def.SkillsReader) Option {
	return func(h *Host) { h.skillset = append(h.skillset, readers...) }
}

// SectionsFunc returns the sections of the prompt of the session id, in
// order. See [Sections].
type SectionsFunc func(ctx context.Context, id string) ([]ai.Section, error)

// Sections sets the function that returns the sections of the prompt
// of each session. The host calls fn before each run, and never keeps
// the result in the config, so a change of the state behind fn reaches
// the next run. The driver sends only the sections that changed. If fn
// returns an error, the run fails. Without Sections, a session gets
// only the prompt of its agent file.
func Sections(fn SectionsFunc) Option {
	return func(h *Host) { h.sections = fn }
}

// IDs sets the function that makes the ID of a session that the caller
// gives no ID. The store names a file with the ID, so fn must give an
// ID with no path. Without IDs, the host makes a UUIDv4.
func IDs(fn func() string) Option {
	return func(h *Host) { h.newID = fn }
}

// Open returns a host that resolves the model spec of each session with
// cat. The catalog must have a driver for each config, or a default
// driver, before the agent of the config starts.
//
// Open fails with [ErrInvalid] without [Tools].
//
// Open reads each session of the store. No agent survives a restart, so
// a session that ran or waited becomes idle, with [StopRestarted].
func Open(ctx context.Context, cat *catalog.Catalog, opts ...Option) (*Host, error) {
	h := &Host{
		cat:      cat,
		newID:    uuid.NewString,
		sessions: make(map[string]*hosted),
		hub: newHub(
			State{Sessions: make(map[string]Session)},
			(*State).Apply,
			State.Clone,
		),
	}
	for _, opt := range opts {
		opt(h)
	}
	if !h.toolList {
		return nil, fmt.Errorf("host: no tool list; give one with host.Tools: %w", ErrInvalid)
	}
	if h.store == nil {
		h.store = session.NewMemoryStore()
	}

	snaps, err := h.store.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("host: list the sessions: %w", err)
	}
	for _, snap := range snaps {
		sess, err := h.recover(ctx, snap)
		if err != nil {
			return nil, err
		}
		h.sessions[sess.ID] = newHosted(h, sess)
		h.hub.publish(Event{Session: sess})
	}
	return h, nil
}

// Config defines the agent of a session. A zero field keeps the default
// of the driver. [Host.Create] keeps the config in the session, and each
// start of the agent reads it there.
type Config struct {
	// ID is the ID of the session. If it is empty, Create makes a new
	// ID with the function of [IDs].
	ID string `json:"id,omitempty"`
	// Dirs are folders with agents/, skills/, and AGENTS.md, lowest
	// first. A later folder wins for a name. The host reads only the
	// file of Agent. The driver gets Dirs.
	Dirs []string `json:"dirs,omitempty"`
	// Agent names an agent file in Dirs. The file fills each empty field
	// of Model, Mode, Effort, and a nil ToolNames. Its disallowed tools
	// go after Deny, and its skills go after Skills. Create reads these
	// once, so an edit of them changes only the sessions that start
	// after it. The prompt of the file is the section [AgentSection],
	// before the sections of [Sections]. The host reads the prompt
	// before each run, so an edit of the prompt reaches the next run.
	// The host does not read the maxTurns key of the file.
	Agent string `json:"agent,omitempty"`
	// Model is the model spec, such as "claude-code/claude-opus-5-5".
	// It picks the driver. It is required if Agent gives no model.
	Model string `json:"model"`
	// Effort is the thinking level of each model call. If it is empty,
	// the driver keeps the default of the model. A value that is not a
	// level fails Create.
	Effort ai.ThinkingLevel `json:"effort,omitempty"`
	// Mode is the permission mode of the agent. If it is empty, the
	// driver keeps its own mode. A driver that cannot honor the mode
	// fails the start of the agent.
	Mode agent.Mode `json:"mode,omitempty"`
	// Dir is the working directory of the agent.
	Dir string `json:"dir,omitempty"`
	// Skills names the skills that the agent loads at the start.
	Skills []string `json:"skills,omitempty"`
	// ToolNames names the tools of the host that the agent can call, as
	// the tools key of an agent file does. A name can be a gopi name or a
	// Claude Code name, such as "Glob". If ToolNames is nil, the agent
	// keeps each tool of [Tools]. A name of no tool of the host fails
	// Create and Update with [tool.ErrUnknownTool].
	ToolNames []string `json:"tools,omitempty"`
	// Allow, Ask, and Deny are the permission rules of the agent, in
	// the format "Tool" or "Tool(content)". A rule can use a gopi name
	// or a Claude Code name, and a rule on "Read" also covers grep and
	// find. The host keeps the names as the caller wrote them, and the
	// driver reads them. A call that the mode and the rules do not
	// allow waits for [Host.Confirm].
	Allow []string `json:"allow,omitempty"`
	Ask   []string `json:"ask,omitempty"`
	Deny  []string `json:"deny,omitempty"`
	// MCP are the MCP servers of the agent. The host keeps them in the
	// session file with the rest of the config, so a header such as a
	// bearer token is on disk.
	MCP []agent.MCPServer `json:"mcp,omitempty"`
	// AutoCompact is the size of the context, in tokens, that starts a
	// compaction. 0 keeps the default of the driver.
	AutoCompact int `json:"auto_compact,omitempty"`
}

// Create makes the session of config, keeps config in it, and starts its
// agent. The session is idle, and [Host.Send] gives it its first
// message.
//
// If the store has a session with the ID, Create returns [ErrExists].
// If the host closed, Create returns [ErrClosed]. If the driver fails,
// Create returns its error. The session then stays idle, with
// [StopError], and the next message starts the agent again.
func (h *Host) Create(ctx context.Context, config Config) (Session, error) {
	config.ID = cmp.Or(config.ID, h.newID())
	fromFile := config.ToolNames == nil && config.Agent != ""
	config, err := withAgent(ctx, config)
	if err == nil {
		err = checkEffort(config)
	}
	if err == nil {
		err = h.checkTools(config, fromFile)
	}
	if err == nil {
		_, _, err = h.cat.Driver(config.Model)
	}
	if err != nil {
		return Session{}, fmt.Errorf("host: session %s: %w", config.ID, err)
	}

	s, err := h.add(ctx, config)
	if err != nil {
		return Session{}, err
	}
	err = s.startAgent(ctx)
	return s.snapshot(), err
}

// checkTools returns an error that wraps [tool.ErrUnknownTool] for a
// name of config that names no tool of the host. fromFile reports that
// the names came from the agent file, so the error names the file.
func (h *Host) checkTools(config Config, fromFile bool) error {
	_, err := tool.Select(h.tools, config.ToolNames)
	if err == nil {
		return nil
	}
	err = fmt.Errorf("%w; add the tool to host.Tools", err)
	if fromFile {
		return fmt.Errorf("agent file %q: %w", config.Agent, err)
	}
	return err
}

// add writes the session of config to the store, and adds it to the
// index. It holds the lock of the host during the writes, so two calls
// cannot add one ID.
func (h *Host) add(ctx context.Context, config Config) (*hosted, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, ErrClosed
	}
	if _, ok := h.sessions[config.ID]; ok {
		return nil, fmt.Errorf("host: session %s: %w", config.ID, ErrExists)
	}
	created := now()
	err := h.store.Create(ctx, session.Session{
		ID:        config.ID,
		CreatedAt: created,
	})
	if errors.Is(err, session.ErrExists) {
		return nil, fmt.Errorf("host: session %s: %w", config.ID, ErrExists)
	}
	if err != nil {
		return nil, fmt.Errorf("host: create session %s: %w", config.ID, err)
	}

	sess := Session{
		ID:        config.ID,
		Status:    StatusIdle,
		Model:     config.Model,
		Mode:      config.Mode,
		Effort:    config.Effort,
		CreatedAt: created,
		UpdatedAt: created,
	}
	if err := setStored(ctx, h.store, config.ID, configValue, config); err != nil {
		return nil, err
	}
	if err := setStored(ctx, h.store, config.ID, sessionValue, sess); err != nil {
		return nil, err
	}
	s := newHosted(h, sess)
	h.sessions[config.ID] = s
	h.hub.publish(Event{Session: sess})
	return s, nil
}

// session returns the session id, or [ErrNotFound].
func (h *Host) session(id string) (*hosted, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.sessions[id]
	if !ok {
		return nil, fmt.Errorf("host: session %s: %w", id, ErrNotFound)
	}
	return s, nil
}

// all returns each session of the host.
func (h *Host) all() []*hosted {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Collect(maps.Values(h.sessions))
}

// Get returns the session id. If the host does not know the
// session, ok is false.
func (h *Host) Get(id string) (Session, bool) {
	s, err := h.session(id)
	if err != nil {
		return Session{}, false
	}
	return s.snapshot(), true
}

// List returns each session, by ID.
func (h *Host) List() []Session {
	sessions := h.all()
	out := make([]Session, len(sessions))
	for i, s := range sessions {
		out[i] = s.snapshot()
	}
	slices.SortFunc(out, func(a, b Session) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

// Log returns the log of the session id, to read its entries and to set
// its values. While the agent runs, it is the log of the agent, so the
// agent and the store agree. Otherwise it is the log in the store. The
// session of a subagent has no agent in the host: the run of its parent
// writes it, so Log gives a copy of the store to read, and a caller does
// not write to it.
func (h *Host) Log(ctx context.Context, id string) (session.Writer, error) {
	s, err := h.session(id)
	if err != nil {
		return nil, err
	}
	return s.log(ctx)
}

// Stream returns the state of the agent of the session id, and the
// stream of its agent events after that state. The stream has the
// events of each run, also of a run that starts after the call. A
// reader applies each event to the state with [agent.State.Apply].
//
// The state is a copy for a viewer: [agent.State.Steer] on it does
// nothing. To steer, call [Host.Send].
//
// The stream ends when the host closes, with the error of ctx when ctx
// ends, or with [ErrSlowReader]. After ErrSlowReader, call Stream
// again.
func (h *Host) Stream(ctx context.Context, id string) (agent.State, iter.Seq2[agent.Event, error], error) {
	s, err := h.session(id)
	if err != nil {
		return agent.State{}, nil, err
	}
	state, events := s.agents.subscribe(ctx)
	return state, events, nil
}

// Events returns each session, and the stream of the
// changes after it. Each event has the new value of one session. A
// reader applies each event to the state with [State.Apply].
//
// The stream ends when the host closes, with the error of ctx when ctx
// ends, or with [ErrSlowReader]. After ErrSlowReader, call Events
// again.
func (h *Host) Events(ctx context.Context) (State, iter.Seq2[Event, error]) {
	return h.hub.subscribe(ctx)
}

// Close stops the agent of each session, and then ends each stream of
// the host. A run that Close cuts ends with [StopRestarted]. The logs
// stay. A second Close does nothing. Do not call Close from a hook,
// because Close waits for each run.
func (h *Host) Close() error {
	h.mu.Lock()
	closed := h.closed
	h.closed = true
	h.mu.Unlock()
	if closed {
		return nil
	}

	var errs []error
	for _, s := range h.all() {
		errs = append(errs, s.stop())
	}
	h.hub.close()
	return errors.Join(errs...)
}
