package codex

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"
	"unicode"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/internal/emit"
	"github.com/sonnes/gopi/pkg/ai"
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
	// a is the agent of the run. Its store keeps the child sessions, and
	// its name is the author of every entry of a child.
	a *Agent
	// root is the turn of the run.
	root *turn
	// byThread holds each child, by its thread ID.
	byThread map[string]*child
	// early holds the notifications of a thread that is not known yet,
	// by thread ID. A child thread can report before its spawn item
	// reaches the turn.
	early map[string][]message
	// late holds each direct child that ended after the turn of the run
	// completed. The model did not see its result.
	late []*child
	// stopping is set by an interrupt. A child whose turn starts after
	// it stops at once.
	stopping bool
}

// child is one subagent of the CLI.
type child struct {
	callID    string
	name      string
	model     string
	sessionID string
	owner     *turn
	turn      *turn
	// running is true from the spawn of the child, or the start of a
	// later turn, to the end of its turn.
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
		a:        a,
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
	ref := agent.ParentRef{
		SessionID:  t.sessionID,
		ToolCallID: it.ID,
	}
	log, err := emit.OpenChild(ctx, s.a.store, id, ref)
	if err != nil {
		return err
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
			emitter:   emit.New(log, s.a.cfg.name, emit.NewState(log), push),
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
	c.turn.reset()
	// Each run of a child reports the usage of its own turn.
	c.turn.usage = ai.Usage{}
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
			return fmt.Errorf("codex: %s: %w", m.Method, err)
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
	if c.turn.subs.stopping {
		_, err := c.turn.conn.request("turn/interrupt", turnInterruptParams{
			ThreadID: c.turn.threadID,
			TurnID:   turnID,
		})
		return err
	}
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
// owner. The text of the end is the last text of c, or the error of a
// child that failed.
func (s *subagents) finish(ctx context.Context, c *child, status string, err error) {
	msgs, _ := c.turn.emitter.Finish(ctx, c.turn.usage, err)
	c.running = false
	c.status = status
	c.text = agent.LastText(msgs)
	if status == agent.SubagentFailed && err != nil {
		c.text = err.Error()
	}
	c.owner.emitter.Subagent(agent.EventSubagentEnd{
		ToolCallID: c.callID,
		SessionID:  c.sessionID,
		Status:     c.status,
		Text:       c.text,
	})
}

// children returns each child, by its thread ID, so that the children
// end and stop in the same order on each run.
func (s *subagents) children() []*child {
	out := slices.Collect(maps.Values(s.byThread))
	slices.SortFunc(out, func(a, b *child) int { return cmp.Compare(a.turn.threadID, b.turn.threadID) })
	return out
}

// running reports whether a child runs.
func (s *subagents) running() bool {
	for _, c := range s.byThread {
		if c.running {
			return true
		}
	}
	return false
}

// interrupt asks the server to stop the turn of each child that runs.
func (s *subagents) interrupt() error {
	s.stopping = true
	var errs []error
	for _, c := range s.children() {
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
	for _, c := range s.children() {
		if c.owner == s.root {
			s.endTree(ctx, c)
		}
	}
}

// endTree ends each child of c that runs, then c if it runs.
func (s *subagents) endTree(ctx context.Context, c *child) {
	for _, d := range s.children() {
		if d.owner == c.turn {
			s.endTree(ctx, d)
		}
	}
	if c.running {
		s.finish(ctx, c, agent.SubagentInterrupted, errRunEnded)
	}
}

// errRunEnded is the error of a child that the end of the run stops.
var errRunEnded = errors.New("codex: the run ended before the subagent")

// report returns the text of the turn that gives the model the result
// of each child that ended after the turn of the run, and forgets
// them. The result of a child that failed is its error. It is "" if no
// child ended late.
func (s *subagents) report() string {
	parts := make([]string, 0, len(s.late))
	for _, c := range s.late {
		label := "Result"
		if c.status == agent.SubagentFailed {
			label = "Error"
		}
		text := "Subagent " + c.status + ": " + c.name
		if c.text != "" {
			text += "\n\n" + label + ":\n" + c.text
		}
		parts = append(parts, text)
	}
	s.late = nil
	return strings.Join(parts, "\n\n")
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
