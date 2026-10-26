package codex

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"strings"
	"unicode"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/internal/emit"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
)

// emptyObject is the JSON of an empty object.
const emptyObject = "{}"

// defaultChildName is the name of a child whose spawn has no agent
// path.
const defaultChildName = "agent"

// subagents tracks the subagents of one run. A Codex subagent is a
// thread of its own in the same server:
//
//	item/started    subAgentActivity started   (the spawn_agent call)
//	turn/started    on the thread of the child
//	item/...        on the thread of the child
//	turn/completed  on the thread of the child
//	item/...        collabAgentToolCall wait   (on the parent thread)
//
// The turn of the run routes each notification of a child thread to
// the turn of that child, which writes to the log of the child. The
// server does not send the prompt of a spawn, so a child has no input.
type subagents struct {
	// store keeps the child sessions. It is nil for an unsaved agent.
	store session.Storer
	// name is the author of every entry of a child.
	name string
	// root is the turn of the run.
	root *turn
	// byThread holds each child, by its thread ID.
	byThread map[string]*child
	// early holds the notifications of a thread that is not known yet,
	// by thread ID. A child thread can report before its spawn item
	// reaches the turn.
	early map[string][]message
	// late holds each child that ended after the turn of the run
	// completed. The model did not see its result.
	late []*child
}

// child is one subagent of the CLI.
type child struct {
	callID    string
	name      string
	model     string
	sessionID string
	owner     *turn
	turn      *turn
	// running is true while a turn of the child runs.
	running bool
	// status and text are the status and the last text of the last run
	// of the child.
	status string
	text   string
}

// newSubagents returns the tracker of the subagents of the run of a,
// whose turn is root.
func newSubagents(a *Agent, root *turn) *subagents {
	return &subagents{
		store:    a.store,
		name:     a.cfg.name,
		root:     root,
		byThread: map[string]*child{},
		early:    map[string][]message{},
	}
}

// open opens the child of a spawn item that the turn t started. Its
// start goes on the stream of t, and the notifications of its thread
// that came early apply now.
func (s *subagents) open(ctx context.Context, t *turn, it item) error {
	threadID := it.AgentThreadID
	if threadID == "" {
		return nil
	}
	if _, ok := s.byThread[threadID]; ok {
		return nil
	}
	name := path.Base(it.AgentPath)
	if it.AgentPath == "" || name == "/" || name == "." {
		name = defaultChildName
	}
	model := ""
	if it.Model != nil {
		model = *it.Model
	}
	id := agent.ChildID(t.sessionID, name, threadID)

	log, err := s.log(ctx, id)
	if err != nil {
		return err
	}
	if len(log.Entries()) == 0 {
		ref := agent.ParentRef{
			SessionID:  t.sessionID,
			ToolCallID: it.ID,
		}
		if err := agent.Parent.Set(ctx, log.Writer(), ref); err != nil {
			return err
		}
	}

	owner := t.emitter
	callID := it.ID
	push := func(ev agent.Event) {
		owner.Subagent(agent.EventSubagent{
			ToolCallID: callID,
			SessionID:  id,
			Event:      ev,
		})
	}
	c := &child{
		callID:    callID,
		name:      name,
		model:     model,
		sessionID: id,
		owner:     t,
		turn: &turn{
			conn:      t.conn,
			emitter:   emit.New(log, s.name, emit.NewState(log), push),
			threadID:  threadID,
			model:     model,
			subs:      s,
			sessionID: id,
		},
	}
	s.byThread[threadID] = c
	if err := c.start(); err != nil {
		return err
	}

	early := s.early[threadID]
	delete(s.early, threadID)
	for _, m := range early {
		if _, err := s.root.dispatch(ctx, m, false); err != nil {
			return err
		}
	}
	return nil
}

// log returns the log of the child id. A child of an unsaved agent has
// an unsaved log.
func (s *subagents) log(ctx context.Context, id string) (*session.Log, error) {
	if id == "" || s.store == nil {
		return session.New(), nil
	}
	return session.Open(ctx, s.store, id)
}

// start sends the start of c on the stream of its owner, and starts the
// run of c.
func (c *child) start() error {
	c.owner.emitter.Subagent(agent.EventSubagentStart{
		ToolCallID: c.callID,
		SessionID:  c.sessionID,
		Name:       c.name,
		Model:      c.model,
	})
	c.running = true
	c.turn.started = map[string]bool{}
	c.turn.done = map[string]bool{}
	return c.turn.emitter.Start()
}

// route applies one notification of a thread that is not the thread of
// the run. A notification of an unknown thread waits until its child
// opens.
func (s *subagents) route(ctx context.Context, m message, p params) error {
	c, ok := s.byThread[p.ThreadID]
	if !ok {
		s.early[p.ThreadID] = append(s.early[p.ThreadID], m)
		return nil
	}
	switch m.Method {
	case methodTurnStarted:
		return c.begin(p.turnOf())
	case methodTurnCompleted:
		return s.end(ctx, c, p)
	}
	if c.turn.turnID == "" {
		return nil
	}
	if !c.turn.ours(p) {
		return nil
	}
	n := note{
		method: m.Method,
		params: p,
	}
	if len(p.Item) > 0 {
		n.item = &item{}
		if err := json.Unmarshal(p.Item, n.item); err != nil {
			return err
		}
	}
	return c.turn.handle(ctx, n)
}

// begin starts a turn of c. A child that ended starts again, as after
// a sendInput call.
func (c *child) begin(turnID string) error {
	if !c.running {
		if err := c.start(); err != nil {
			return err
		}
	}
	c.turn.turnID = turnID
	return nil
}

// end ends the turn of c at its turn/completed, and sends the end on
// the stream of its owner. The text of the end is the last text of the
// child.
func (s *subagents) end(ctx context.Context, c *child, p params) error {
	if !c.running {
		return nil
	}
	runErr := c.turn.complete(ctx, p, false)
	status := agent.SubagentCompleted
	if p.Turn != nil {
		switch p.Turn.Status {
		case turnCompleted:
		case turnInterrupted:
			status = agent.SubagentInterrupted
		default:
			status = agent.SubagentFailed
		}
	}
	if runErr != nil && status == agent.SubagentCompleted {
		status = agent.SubagentFailed
	}
	s.finish(ctx, c, status, runErr)
	if c.owner == s.root && s.root.completed {
		s.late = append(s.late, c)
	}
	return nil
}

// finish ends the run of c, and sends its end on the stream of its
// owner.
func (s *subagents) finish(ctx context.Context, c *child, status string, err error) {
	msgs, _ := c.turn.emitter.Finish(ctx, c.turn.usage, err)
	c.running = false
	c.status = status
	c.text = lastText(msgs)
	c.owner.emitter.Subagent(agent.EventSubagentEnd{
		ToolCallID: c.callID,
		SessionID:  c.sessionID,
		Status:     c.status,
		Text:       c.text,
	})
}

// running reports whether a child runs.
func (s *subagents) running() bool {
	if s == nil {
		return false
	}
	for _, c := range s.byThread {
		if c.running {
			return true
		}
	}
	return false
}

// interrupt asks the server to stop the turn of each child that runs.
func (s *subagents) interrupt() error {
	var errs []error
	for _, c := range s.byThread {
		if !c.running || c.turn.turnID == "" {
			continue
		}
		_, err := c.turn.conn.request("turn/interrupt", turnInterruptParams{
			ThreadID: c.turn.threadID,
			TurnID:   c.turn.turnID,
		})
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// endAll ends each child that runs as interrupted, at the end of a run
// that did not wait for them. A nested child ends before its owner, so
// that the stream of the owner closes after it.
func (s *subagents) endAll(ctx context.Context) {
	for s.running() {
		for _, c := range s.byThread {
			if c.running && !s.owner(c) {
				s.finish(ctx, c, agent.SubagentInterrupted, errRunEnded)
				break
			}
		}
	}
}

// errRunEnded is the error of a child that the end of the run stops.
var errRunEnded = errors.New("codex: the run ended before the subagent")

// owner reports whether c owns a child that runs.
func (s *subagents) owner(c *child) bool {
	for _, d := range s.byThread {
		if d.running && d.owner == c.turn {
			return true
		}
	}
	return false
}

// report returns the text of the turn that gives the model the result
// of each child that ended after the turn of the run, and forgets
// them. It is "" if no child ended late.
func (s *subagents) report() string {
	parts := make([]string, 0, len(s.late))
	for _, c := range s.late {
		text := "Subagent " + c.status + ": " + c.name
		if c.text != "" {
			text += "\n\nResult:\n" + c.text
		}
		parts = append(parts, text)
	}
	s.late = nil
	return strings.Join(parts, "\n\n")
}

// lastText returns the text of the last assistant message with text.
func lastText(msgs []ai.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role != ai.RoleAssistant {
			continue
		}
		if text := m.Text(); text != "" {
			return text
		}
	}
	return ""
}

// collabToolName returns the name that the model sees for a collab
// call: the tool in snake case, such as send_input for sendInput.
func collabToolName(tool string) string {
	var b strings.Builder
	for i, r := range tool {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('_')
			}
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}
