package claude

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"

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
}

// call is one tool call of the run, and the runner that started it.
type call struct {
	owner *runner
	tc    ai.ToolCall
}

// child is one subagent of the CLI.
type child struct {
	callID    string
	owner     *runner
	run       *runner
	sessionID string
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
		tasks: &tasks{
			calls:   map[string]call{},
			started: map[string]*line{},
			waiting: map[string][]*line{},
			open:    map[string]*child{},
		},
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

	owner := c.owner.em
	callID := l.ToolUseID
	push := func(ev agent.Event) {
		owner.Subagent(agent.EventSubagent{
			ToolCallID: callID,
			SessionID:  id,
			Event:      ev,
		})
	}
	em := emit.New(log, r.a.name, emit.NewState(log), push)
	owner.Subagent(agent.EventSubagentStart{
		ToolCallID: callID,
		SessionID:  id,
		Name:       name,
		Title:      l.Description,
		Prompt:     l.Prompt,
		Model:      in.Model,
	})
	if err := em.Start(); err != nil {
		owner.Subagent(agent.EventSubagentEnd{
			ToolCallID: callID,
			SessionID:  id,
			Status:     agent.SubagentFailed,
			Text:       err.Error(),
		})
		return err
	}

	// The child is open from its start, so each exit ends it.
	run := &runner{
		a:         r.a,
		em:        em,
		sessionID: id,
		subagents: map[string]bool{},
		tasks:     r.tasks,
	}
	r.tasks.open[callID] = &child{
		callID:    callID,
		owner:     c.owner,
		run:       run,
		sessionID: id,
	}
	if err := em.Input(store, []session.Entry{session.Text(l.Prompt)}); err != nil {
		return err
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

// callEnded ends the child of a call whose result came before its
// task_notification line. The result is not the text of the child: the
// CLI wraps the report of the child in a frame for the parent. So the
// child ends with no last message.
func (r *runner) callEnded(ctx context.Context, id string, result ai.ToolResult) error {
	c, ok := r.tasks.open[id]
	if !ok {
		return nil
	}
	status := agent.SubagentCompleted
	if result.IsError {
		status = agent.SubagentFailed
	}
	return r.endChild(ctx, c, status, "")
}

// endChild ends the run of c and sends its end on the stream of its
// owner. Each open child of c ends first, so that the stream of c closes
// before its end. The CLI does not send the last message of a subagent as
// a line, so a completed child gets text as its last assistant message.
// A failed write of the log does not stop the end: the child still ends,
// as failed, and endChild returns the error.
func (r *runner) endChild(ctx context.Context, c *child, status, text string) error {
	var errs []error
	for _, inner := range r.openChildren() {
		if inner.owner == c.run {
			errs = append(errs, r.endChild(ctx, inner, agent.SubagentInterrupted, ""))
		}
	}
	delete(r.tasks.open, c.callID)

	store := context.WithoutCancel(ctx)
	run := c.run
	errs = append(errs, run.finish(store))
	if status == agent.SubagentCompleted && text != "" {
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

	c.owner.em.Subagent(agent.EventSubagentEnd{
		ToolCallID: c.callID,
		SessionID:  c.sessionID,
		Status:     status,
		Text:       text,
	})
	return errors.Join(errs...)
}

// endChildren ends each open child as interrupted, at the end of a run,
// and returns the errors of their logs.
func (r *runner) endChildren(ctx context.Context) error {
	var errs []error
	for len(r.tasks.open) > 0 {
		c := r.openChildren()[0]
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
