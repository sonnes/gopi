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

// The status values of task_notification.
const (
	taskCompleted = "completed"
	taskFailed    = "failed"
)

// tasks tracks the subagents of one run. The CLI sends a subagent as
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
type tasks struct {
	// calls are the tool calls that started in the run, by ID, with the
	// runner that started them.
	calls map[string]call
	// started are the task_started lines whose call did not start yet,
	// by the ID of the call.
	started map[string]*line
	// waiting are the lines of a child that is not open yet, by the ID
	// of its call.
	waiting map[string][]*line
	// open are the children that run, by the ID of their call.
	open map[string]*child
	// async are the Agent calls whose result said that the child runs in
	// the background, before the child opened.
	async map[string]bool
}

// call is one tool call of the run, and the runner that started it.
type call struct {
	owner *runner
	tc    ai.ToolCall
}

// child is one subagent of the CLI. A child in the background outlives
// the run that started it: the agent keeps it, and each later run, or
// the reader between runs, carries it.
type child struct {
	callID    string
	sessionID string
	name      string
	title     string
	prompt    string
	model     string
	log       *session.Log
	// owner is the runner whose call started the child, in the run that
	// carries it. It is nil between runs.
	owner *runner
	// run maps the lines of the child to its log. send takes the start,
	// each event, and the end of the child, for the run that carries it.
	run  *runner
	send func(agent.Event)
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
	id := ""
	if a.store != nil {
		id = a.log.Session().ID
	}
	return &runner{
		a:         a,
		proc:      proc,
		em:        em,
		sessionID: id,
		subagents: map[string]bool{},
		tasks:     newTasks(),
	}
}

// newTasks returns the tracker of the subagents of one run.
func newTasks() *tasks {
	return &tasks{
		calls:   map[string]call{},
		started: map[string]*line{},
		waiting: map[string][]*line{},
		open:    map[string]*child{},
		async:   map[string]bool{},
	}
}

// callStarted records a tool call that r started. If its task_started
// line came first, the child opens now.
func (r *runner) callStarted(ctx context.Context, tc ai.ToolCall) error {
	r.tasks.calls[tc.ID] = call{
		owner: r,
		tc:    tc,
	}
	l, ok := r.tasks.started[tc.ID]
	if !ok {
		return nil
	}
	delete(r.tasks.started, tc.ID)
	return r.openChild(ctx, l)
}

// taskStarted opens the child of a task_started line, or keeps the line
// until its call starts. A task that is not a subagent opens nothing.
func (r *runner) taskStarted(ctx context.Context, l *line) error {
	if l.TaskType != taskTypeAgent || l.ToolUseID == "" {
		return nil
	}
	if _, ok := r.tasks.calls[l.ToolUseID]; !ok {
		r.tasks.started[l.ToolUseID] = l
		return nil
	}
	return r.openChild(ctx, l)
}

// openChild opens the session of the child of l, sends its start on
// the stream of its owner, stores its prompt, and applies the lines
// that waited for it.
func (r *runner) openChild(ctx context.Context, l *line) error {
	c := r.tasks.calls[l.ToolUseID]
	var in agentInput
	_ = json.Unmarshal(c.tc.Input, &in)
	name := in.SubagentType
	if name == "" {
		name = defaultChildName
	}
	id := agent.ChildID(c.owner.sessionID, name, l.TaskID)

	// The CLI goes on after an interrupt, so the child keeps its lines.
	store := context.WithoutCancel(ctx)
	log, err := r.childLog(store, id)
	if err != nil {
		return err
	}
	if len(log.Entries()) == 0 {
		ref := agent.ParentRef{
			SessionID:  c.owner.sessionID,
			ToolCallID: l.ToolUseID,
		}
		if err := agent.Parent.Set(store, log.Writer(), ref); err != nil {
			return err
		}
	}

	ch := &child{
		callID:    l.ToolUseID,
		sessionID: id,
		name:      name,
		title:     l.Description,
		prompt:    l.Prompt,
		model:     in.Model,
		log:       log,
	}
	if r.tasks.async[l.ToolUseID] {
		delete(r.tasks.async, l.ToolUseID)
		ch.background = true
	}
	return r.attach(ctx, ch, c.owner, c.owner.em.Subagent, true)
}

// attach opens ch in the run of r, under owner, and sends its start to
// send. A new child stores its prompt. A child in the background that an
// earlier run started comes back with a new start, so that the stream
// of each run is balanced. Then attach applies the lines that waited.
func (r *runner) attach(ctx context.Context, ch *child, owner *runner, send func(agent.Event), isNew bool) error {
	store := context.WithoutCancel(ctx)
	callID := ch.callID
	id := ch.sessionID
	push := func(ev agent.Event) {
		send(agent.EventSubagent{
			ToolCallID: callID,
			SessionID:  id,
			Event:      ev,
		})
	}
	em := emit.New(ch.log, r.a.name, emit.NewState(ch.log), push)
	send(agent.EventSubagentStart{
		ToolCallID: callID,
		SessionID:  id,
		Name:       ch.name,
		Title:      ch.title,
		Prompt:     ch.prompt,
		Model:      ch.model,
	})
	if err := em.Start(); err != nil {
		send(agent.EventSubagentEnd{
			ToolCallID: callID,
			SessionID:  id,
			Status:     agent.SubagentFailed,
			Text:       err.Error(),
		})
		return err
	}

	// The child is open from its start, so each exit ends it.
	ch.owner = owner
	ch.send = send
	ch.run = &runner{
		a:         r.a,
		em:        em,
		sessionID: id,
		subagents: map[string]bool{},
		tasks:     r.tasks,
	}
	r.tasks.open[callID] = ch
	if isNew {
		if err := em.Input(store, []session.Entry{session.Text(ch.prompt)}); err != nil {
			return err
		}
	}
	waiting := r.tasks.waiting[callID]
	delete(r.tasks.waiting, callID)
	for _, wl := range waiting {
		if err := r.childLine(ctx, callID, wl); err != nil {
			return err
		}
	}
	return nil
}

// childLog returns the log of the child id. A child of an unsaved
// agent has an unsaved log.
func (r *runner) childLog(ctx context.Context, id string) (*session.Log, error) {
	if id == "" {
		return session.New(), nil
	}
	return session.Open(ctx, r.a.store, id)
}

// childLine applies one line of the child of the call callID. A line of
// a child that is not open waits. The child keeps its assistant
// messages and its tool results. Its streamed events are not applied,
// because the assistant lines carry the same blocks.
func (r *runner) childLine(ctx context.Context, callID string, l *line) error {
	c, ok := r.tasks.open[callID]
	if !ok {
		r.tasks.waiting[callID] = append(r.tasks.waiting[callID], l)
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
func (r *runner) taskEnded(ctx context.Context, l *line) error {
	c, ok := r.tasks.open[l.ToolUseID]
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
	return r.endChild(ctx, c, status, l.Summary)
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

// callEnded ends the child of a call whose result came before its
// task_notification line. The result is not the text of the child: the
// CLI wraps the report of the child in a frame for the parent. So the
// child ends with no last message. A result that says that the child
// runs in the background ends nothing: only task_notification ends a
// child in the background.
func (r *runner) callEnded(ctx context.Context, id string, result ai.ToolResult) error {
	async := !result.IsError && launchedAsync(result)
	c, ok := r.tasks.open[id]
	if !ok {
		if async {
			r.tasks.async[id] = true
		}
		return nil
	}
	if async {
		c.background = true
		return nil
	}
	status := agent.SubagentCompleted
	if result.IsError {
		status = agent.SubagentFailed
	}
	return r.endChild(ctx, c, status, "")
}

// endChild ends the run of c and sends its end. Each open child of c
// ends first, so that the stream of c closes before its end. The CLI
// does not send the last message of a foreground subagent as a line, so
// a completed child gets text as its last assistant message, unless its
// log already ends with it. A failed write of the log does not stop the
// end: the child still ends, as failed, and endChild returns the error.
func (r *runner) endChild(ctx context.Context, c *child, status, text string) error {
	var errs []error
	for _, inner := range r.openChildren() {
		if inner.owner == c.run {
			errs = append(errs, r.endChild(ctx, inner, agent.SubagentInterrupted, ""))
		}
	}
	delete(r.tasks.open, c.callID)
	r.a.forget(c.callID)

	store := context.WithoutCancel(ctx)
	run := c.run
	errs = append(errs, run.finish(store))
	if status == agent.SubagentCompleted && text != "" && lastText(c.log) != text {
		final := ai.Message{
			Role:       ai.RoleAssistant,
			Content:    []ai.Content{ai.Text{Text: text}},
			StopReason: ai.StopReasonStop,
		}
		errs = append(errs, run.em.End(store, final))
	}
	failed := errors.Join(errs...)
	if failed != nil {
		status = agent.SubagentFailed
		text = failed.Error()
	}

	var runErr error
	if status != agent.SubagentCompleted {
		runErr = subagentError(status)
	}
	if _, err := run.em.Finish(store, run.em.Usage(), runErr); err != nil && !errors.Is(err, runErr) {
		errs = append(errs, err)
	}

	c.send(agent.EventSubagentEnd{
		ToolCallID: c.callID,
		SessionID:  c.sessionID,
		Status:     status,
		Text:       text,
	})
	return errors.Join(errs...)
}

// lastText returns the text of the last message of log, or "".
func lastText(log *session.Log) string {
	msgs := log.Messages()
	if len(msgs) == 0 {
		return ""
	}
	return strings.TrimSpace(msgs[len(msgs)-1].Text())
}

// detach ends the part of c that the run of r carries, and the agent
// keeps c: its run ends, and its end has the status background. The
// next run, or the reader between runs, carries it again.
func (r *runner) detach(ctx context.Context, c *child) error {
	delete(r.tasks.open, c.callID)
	store := context.WithoutCancel(ctx)
	err := c.run.finish(store)
	_, ferr := c.run.em.Finish(store, c.run.em.Usage(), nil)
	c.send(agent.EventSubagentEnd{
		ToolCallID: c.callID,
		SessionID:  c.sessionID,
		Status:     agent.SubagentBackground,
	})
	c.owner = nil
	c.run = nil
	c.send = nil
	r.a.keep(c)
	return errors.Join(err, ferr)
}

// endChildren ends each open child at the end of a run, and returns the
// errors of their logs. If keep is true, the CLI goes on, so a child in
// the background goes on too: detach gives it to the agent. Each other
// child ends as interrupted.
func (r *runner) endChildren(ctx context.Context, keep bool) error {
	var errs []error
	for len(r.tasks.open) > 0 {
		c := r.openChildren()[0]
		if keep && c.background {
			errs = append(errs, r.detach(ctx, c))
			continue
		}
		errs = append(errs, r.endChild(ctx, c, agent.SubagentInterrupted, ""))
	}
	return errors.Join(errs...)
}

// openChildren returns the open children, by the ID of their call, so
// that they end in the same order on each run.
func (r *runner) openChildren() []*child {
	out := slices.Collect(maps.Values(r.tasks.open))
	slices.SortFunc(out, func(a, b *child) int { return cmp.Compare(a.callID, b.callID) })
	return out
}

// subagentError is the error of the run of a child that did not
// complete.
type subagentError string

func (e subagentError) Error() string { return "claude: the subagent " + string(e) }
