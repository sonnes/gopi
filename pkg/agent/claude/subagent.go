package claude

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/internal/emit"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
)

// taskTypeAgent is the task type of a subagent. The CLI also reports a
// background shell as a task, with the type local_bash.
const taskTypeAgent = "local_agent"

// defaultChildName is the name of a child whose Agent call names no
// subagent_type.
const defaultChildName = "agent"

// subagentTools are the tools that start a subagent.
var subagentTools = []string{"Agent", "Task"}

// The status values of task_notification.
const (
	taskCompleted = "completed"
	taskFailed    = "failed"
)

// subagents tracks the subagents of the CLI. The CLI sends a subagent as
// task lines, and as assistant and user lines that name the Agent call
// in parent_tool_use_id:
//
//	assistant  tool_use Agent  (id c1)
//	system     task_started    tool_use_id c1, task_id, prompt
//	user       ...             parent_tool_use_id c1
//	assistant  ...             parent_tool_use_id c1
//	system     task_notification  tool_use_id c1, status, summary
//	user       tool_result     tool_use_id c1
//
// The task_started line comes before the message of the Agent call
// ends, so before the call starts on the run stream. The child waits
// for the start of its call. A line of a child that is not open yet
// waits too.
//
// A child in the background outlives the run that started it, so the
// agent keeps one tracker. Its carrier is the run, or the reader between
// runs. Each carrier sends a new start of each child in the background,
// and gives the children back at its end, so that the stream of each
// run is balanced. The carriers take turns, so the tracker has no lock.
type subagents struct {
	a *Agent
	// send is the sink of the carrier. A child of a call of the parent
	// sends its start, its events, and its end to it. It is nil between
	// carriers.
	send func(agent.Event)
	// calls are the tool calls that started under the carrier, by ID,
	// with the runner that started them.
	calls map[string]call
	// started are the task_started lines whose call did not start yet,
	// by the ID of the call.
	started map[string]*line
	// waiting are the lines of a child that is not open yet, by the ID
	// of its call.
	waiting map[string][]*line
	// open are the children that run, by the ID of their call. A child
	// in the background stays open between runs.
	open map[string]*child
}

// call is one tool call of the carrier, and the runner that started it.
type call struct {
	owner *runner
	tc    ai.ToolCall
	// result is the status that the result of the call gave its child:
	// completed, failed, or background. It is "" before the result.
	result string
}

// child is one subagent of the CLI. Its runner and its emitter last from
// its start to its end, also across runs, so the stream of the child has
// one agent_start and one agent_end.
type child struct {
	callID    string
	sessionID string
	name      string
	title     string
	prompt    string
	model     string
	log       *session.Log
	// run maps the lines of the child to its log.
	run *runner
	// owner is the runner of the child whose call started this child.
	// It is nil for a child of a call of the parent.
	owner *runner
	// background is true after the CLI moved the child to the background.
	background bool
}

// agentInput is the part of the input of an Agent call that the child
// reads.
type agentInput struct {
	SubagentType string `json:"subagent_type"`
	Model        string `json:"model"`
}

// newRunner returns the runner of one run of a.
func newRunner(a *Agent, proc *process, em *emit.Emitter) *runner {
	return &runner{
		a:         a,
		proc:      proc,
		em:        em,
		sessionID: a.sessionID(),
		subs:      a.subs,
	}
}

// newSubagents returns the tracker of the subagents of a.
func newSubagents(a *Agent) *subagents {
	return &subagents{
		a:    a,
		open: map[string]*child{},
	}
}

// carry makes send the sink of a new carrier, and sends a new start of
// each child in the background to it.
func (s *subagents) carry(send func(agent.Event)) {
	s.send = send
	s.calls = map[string]call{}
	s.started = map[string]*line{}
	s.waiting = map[string][]*line{}
	for _, c := range s.top() {
		send(c.start())
	}
}

// release gives back the children of the carrier at its end, and
// returns the errors of their logs. If keep is true, the CLI goes on, so
// a child in the background goes on too: it gets an end with the status
// background, and stays open for the next carrier. A child whose call
// has a result ends with the status of that result. Each other child
// ends as interrupted. A second release of the same carrier does
// nothing.
func (s *subagents) release(ctx context.Context, keep bool) error {
	if s.send == nil {
		return nil
	}
	defer func() { s.send = nil }()
	var errs []error
	for _, c := range s.top() {
		result := s.calls[c.callID].result
		ended := result == agent.SubagentCompleted || result == agent.SubagentFailed
		switch {
		case keep && c.background:
			s.send(agent.EventSubagentEnd{
				ToolCallID: c.callID,
				SessionID:  c.sessionID,
				Status:     agent.SubagentBackground,
				Text:       agent.LastText(c.log.Messages()),
			})
		case ended:
			errs = append(errs, s.end(ctx, c, result, ""))
		default:
			errs = append(errs, s.end(ctx, c, agent.SubagentInterrupted, ""))
		}
	}
	return errors.Join(errs...)
}

// callStarted records a tool call that owner started. If its
// task_started line came first, the child opens now.
func (s *subagents) callStarted(ctx context.Context, owner *runner, tc ai.ToolCall) error {
	s.calls[tc.ID] = call{
		owner: owner,
		tc:    tc,
	}
	l, ok := s.started[tc.ID]
	if !ok {
		return nil
	}
	delete(s.started, tc.ID)
	return s.openChild(ctx, l)
}

// taskStarted opens the child of a task_started line, or keeps the line
// until its call starts. A task that is not a subagent opens nothing.
func (s *subagents) taskStarted(ctx context.Context, l *line) error {
	if l.TaskType != taskTypeAgent || l.ToolUseID == "" {
		return nil
	}
	if _, ok := s.calls[l.ToolUseID]; !ok {
		s.started[l.ToolUseID] = l
		return nil
	}
	return s.openChild(ctx, l)
}

// openChild opens the session of the child of l, sends its start,
// stores its prompt, and applies the lines that waited for it.
func (s *subagents) openChild(ctx context.Context, l *line) error {
	c := s.calls[l.ToolUseID]
	var in agentInput
	_ = json.Unmarshal(c.tc.Input, &in)
	name := cmp.Or(in.SubagentType, defaultChildName)
	id := agent.ChildID(c.owner.sessionID, name, l.TaskID)
	ref := agent.ParentRef{
		SessionID:  c.owner.sessionID,
		ToolCallID: l.ToolUseID,
	}

	// The CLI goes on after an interrupt, so the child keeps its lines.
	store := context.WithoutCancel(ctx)
	log, err := emit.OpenChild(store, s.a.store, id, ref)
	if err != nil {
		return err
	}

	ch := &child{
		callID:     l.ToolUseID,
		sessionID:  id,
		name:       name,
		title:      l.Description,
		prompt:     l.Prompt,
		model:      in.Model,
		log:        log,
		background: c.result == agent.SubagentBackground,
	}
	if c.owner.isChild() {
		ch.owner = c.owner
	}
	push := func(ev agent.Event) {
		s.sink(ch)(agent.EventSubagent{
			ToolCallID: ch.callID,
			SessionID:  id,
			Event:      ev,
		})
	}
	em := emit.New(log, s.a.name, emit.NewState(log), push)
	s.sink(ch)(ch.start())
	if err := em.Start(); err != nil {
		s.sink(ch)(agent.EventSubagentEnd{
			ToolCallID: ch.callID,
			SessionID:  id,
			Status:     agent.SubagentFailed,
			Text:       err.Error(),
		})
		return err
	}

	// The child is open from its start, so each exit ends it.
	ch.run = &runner{
		a:         s.a,
		em:        em,
		sessionID: id,
		subs:      s,
	}
	s.open[ch.callID] = ch
	if err := em.Input(store, []session.Entry{session.Text(ch.prompt)}); err != nil {
		return err
	}
	waiting := s.waiting[ch.callID]
	delete(s.waiting, ch.callID)
	for _, wl := range waiting {
		if err := s.line(ctx, ch.callID, wl); err != nil {
			return err
		}
	}
	return nil
}

// start returns the start event of c.
func (c *child) start() agent.EventSubagentStart {
	return agent.EventSubagentStart{
		ToolCallID: c.callID,
		SessionID:  c.sessionID,
		Name:       c.name,
		Title:      c.title,
		Prompt:     c.prompt,
		Model:      c.model,
	}
}

// sink returns the function that takes the start, the events, and the
// end of c: the emitter of the child that owns c, or the sink of the
// carrier. Between carriers, it drops the events.
func (s *subagents) sink(c *child) func(agent.Event) {
	if c.owner != nil {
		return c.owner.em.Subagent
	}
	if s.send == nil {
		return func(agent.Event) {}
	}
	return s.send
}

// line applies one line of the child of the call callID. A line of a
// child that is not open waits. The child keeps its assistant messages
// and its tool results. Its streamed events are not applied, because
// the assistant lines carry the same blocks.
func (s *subagents) line(ctx context.Context, callID string, l *line) error {
	c, ok := s.open[callID]
	if !ok {
		s.waiting[callID] = append(s.waiting[callID], l)
		return nil
	}
	if l.Message == nil {
		return nil
	}
	switch l.Type {
	case "assistant":
		return c.run.assistant(ctx, l.Message)
	case "user":
		return c.run.user(ctx, l.Message)
	}
	return nil
}

// taskEnded ends the child of a task_notification line. The summary is
// the last text of the child.
func (s *subagents) taskEnded(ctx context.Context, l *line) error {
	c, ok := s.open[l.ToolUseID]
	if !ok {
		return nil
	}
	status := agent.SubagentInterrupted
	switch l.Status {
	case taskCompleted:
		status = agent.SubagentCompleted
	case taskFailed:
		status = agent.SubagentFailed
	}
	return s.end(ctx, c, status, l.Summary)
}

// asyncMark starts the text of an Agent result that says that the child
// runs in the background. The CLI gives it to a call that runs in the
// background from its start, and to a call that a move before a steer
// sent to the background.
const asyncMark = "Async agent launched successfully"

// launchedAsync reports whether result says that the child of the call
// runs in the background.
func launchedAsync(result ai.ToolResult) bool {
	text := ai.Message{Content: result.Content}.Text()
	return strings.HasPrefix(text, asyncMark)
}

// callEnded keeps the status that the result of the call id gives its
// child. The result is not the text of the child: the CLI wraps the
// report of the child in a frame for the parent. So the child waits for
// its task_notification, whose summary is its last text. A result that
// says that the child runs in the background marks the child.
func (s *subagents) callEnded(id string, result ai.ToolResult) {
	cl, ok := s.calls[id]
	if !ok {
		return
	}
	switch {
	case result.IsError:
		cl.result = agent.SubagentFailed
	case launchedAsync(result):
		cl.result = agent.SubagentBackground
	default:
		cl.result = agent.SubagentCompleted
	}
	s.calls[id] = cl
	if c, open := s.open[id]; open && cl.result == agent.SubagentBackground {
		c.background = true
	}
}

// end ends the run of c and sends its end. Each open child of c ends
// first, so that the stream of c closes before its end. The CLI does
// not send the last message of a foreground subagent as a line, so a
// completed child gets summary as its last assistant message, unless
// its log already ends with it. The text of the end is the last text of
// the child, or the error of a failed child. A failed write of the log
// does not stop the end: the child still ends, as failed, and end
// returns the error.
func (s *subagents) end(ctx context.Context, c *child, status, summary string) error {
	var errs []error
	for _, inner := range s.children() {
		if inner.owner == c.run {
			errs = append(errs, s.end(ctx, inner, agent.SubagentInterrupted, ""))
		}
	}
	delete(s.open, c.callID)

	store := context.WithoutCancel(ctx)
	run := c.run
	errs = append(errs, run.finish(store))
	addSummary := status == agent.SubagentCompleted &&
		summary != "" &&
		agent.LastText(c.log.Messages()) != summary
	if addSummary {
		final := ai.Message{
			Role:       ai.RoleAssistant,
			Content:    []ai.Content{ai.Text{Text: summary}},
			StopReason: ai.StopReasonStop,
		}
		errs = append(errs, run.em.End(store, final))
	}
	writeErr := errors.Join(errs...)
	if writeErr != nil {
		status = agent.SubagentFailed
	}

	var runErr error
	if status != agent.SubagentCompleted {
		runErr = subagentError(status)
	}
	if _, err := run.em.Finish(store, run.em.Usage(), runErr); err != nil && !errors.Is(err, runErr) {
		errs = append(errs, err)
	}

	text := agent.LastText(c.log.Messages())
	if status == agent.SubagentFailed {
		text = cmp.Or(summary, runErr.Error())
		if writeErr != nil {
			text = writeErr.Error()
		}
	}
	s.sink(c)(agent.EventSubagentEnd{
		ToolCallID: c.callID,
		SessionID:  c.sessionID,
		Status:     status,
		Text:       text,
	})
	return errors.Join(errs...)
}

// fail ends the open child of the call callID as failed with err. The
// reader between runs calls it, because no run there can fail. A child
// that already ended carries its error in its end.
func (s *subagents) fail(ctx context.Context, callID string, err error) {
	c, ok := s.open[callID]
	if err == nil || !ok {
		return
	}
	// The end carries the error of each write that fails in it too.
	_ = s.end(ctx, c, agent.SubagentFailed, err.Error())
}

// foreground returns the IDs of the subagent calls of the parent that
// wait for their result, in order. A steer cancels the child of such a
// call, so the steer moves it to the background first, and the child
// ends its work.
func (s *subagents) foreground() []string {
	var ids []string
	for id, cl := range s.calls {
		waits := cl.result == "" &&
			!cl.owner.isChild() &&
			slices.Contains(subagentTools, cl.tc.Name)
		if waits {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}

// top returns the open children of calls of the parent, by the ID of
// their call.
func (s *subagents) top() []*child {
	var out []*child
	for _, c := range s.children() {
		if c.owner == nil {
			out = append(out, c)
		}
	}
	return out
}

// children returns the open children, by the ID of their call, so that
// they end in the same order on each run.
func (s *subagents) children() []*child {
	out := slices.Collect(maps.Values(s.open))
	slices.SortFunc(out, func(a, b *child) int { return cmp.Compare(a.callID, b.callID) })
	return out
}

// subagentError is the error of the run of a child that did not
// complete.
type subagentError string

func (e subagentError) Error() string { return "claude: the subagent " + string(e) }
