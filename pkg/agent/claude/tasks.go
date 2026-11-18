package claude

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
	"github.com/sonnes/gopi/pkg/tool"
)

// taskTypeShell is the task type of a shell command in the background.
// The CLI also runs a Monitor as a shell command, so only the tool call
// of the task tells the two apart.
const taskTypeShell = "local_bash"

// wakeupSlack is how long a wakeup stays open after its time. The CLI
// starts the turn of a wakeup close to its time, so a wake in this
// window is the wake of the wakeup.
const wakeupSlack = time.Minute

// now returns the current time. A test of the package can replace it.
var now = time.Now

// task is one background task of the CLI, with the tool call that
// started it.
type task struct {
	agent.Task
	callID string
}

// openTask records the shell task of a task_started line, and sends the
// new list.
func (s *subagents) openTask(l *line) {
	if l.TaskType != taskTypeShell || l.TaskID == "" {
		return
	}
	kind := agent.TaskCommand
	if c, ok := s.calls[l.ToolUseID]; ok && c.tc.Name == tool.Monitor {
		kind = agent.TaskMonitor
	}
	s.tasks = append(s.tasks, task{
		Task: agent.Task{
			ID:        l.TaskID,
			Kind:      kind,
			Title:     l.Description,
			StartedAt: now(),
		},
		callID: l.ToolUseID,
	})
	s.sendTasks()
}

// closeTask removes the task id at its task_notification line, and
// sends the new list. It reports whether the task was open.
func (s *subagents) closeTask(id string) bool {
	n := len(s.tasks)
	s.tasks = slices.DeleteFunc(s.tasks, func(t task) bool { return t.ID == id })
	if len(s.tasks) == n {
		return false
	}
	s.sendTasks()
	return true
}

// taskCall applies a tool call that starts a task. A Monitor call makes
// its task a monitor: the task_started line of a call comes before the
// call starts. A ScheduleWakeup call opens a wakeup, which takes the
// place of the last one, because the CLI keeps one wakeup.
func (s *subagents) taskCall(tc ai.ToolCall) {
	switch tc.Name {
	case tool.Monitor:
		for i, t := range s.tasks {
			if t.callID == tc.ID {
				s.tasks[i].Kind = agent.TaskMonitor
				s.sendTasks()
			}
		}
	case tool.ScheduleWakeup:
		s.wakeup(tc)
	}
}

// wakeupInput is the part of the input of a ScheduleWakeup call that the
// agent reads.
type wakeupInput struct {
	DelaySeconds int    `json:"delaySeconds"`
	Reason       string `json:"reason"`
}

// wakeup opens the wakeup of a ScheduleWakeup call. The CLI sends no
// task line for it, so the task ends at a wake at its time.
func (s *subagents) wakeup(tc ai.ToolCall) {
	var in wakeupInput
	_ = json.Unmarshal(tc.Input, &in)
	start := now()
	s.tasks = slices.DeleteFunc(s.tasks, func(t task) bool { return t.Kind == agent.TaskWakeup })
	s.tasks = append(s.tasks, task{
		Task: agent.Task{
			ID:        tc.ID,
			Kind:      agent.TaskWakeup,
			Title:     in.Reason,
			StartedAt: start,
			Due:       start.Add(time.Duration(in.DelaySeconds) * time.Second),
		},
		callID: tc.ID,
	})
	s.sendTasks()
}

// woke closes each wakeup whose time came, at a turn that the CLI
// started by itself.
func (s *subagents) woke() {
	cutoff := now().Add(wakeupSlack)
	n := len(s.tasks)
	s.tasks = slices.DeleteFunc(s.tasks, func(t task) bool {
		return t.Kind == agent.TaskWakeup && !t.Due.After(cutoff)
	})
	if len(s.tasks) != n {
		s.sendTasks()
	}
}

// stopTasks ends each open task at the close of the CLI. It adds one
// [agent.KindStopped] entry that names them to log, and sends the empty
// list.
func (s *subagents) stopTasks(ctx context.Context, log *session.Log) error {
	if len(s.tasks) == 0 {
		return nil
	}
	stopped := agent.Stopped{Tasks: s.list()}
	s.tasks = nil
	s.sendTasks()
	entry, err := session.Custom(agent.KindStopped, stopped)
	if err != nil {
		return err
	}
	_, err = log.Append(ctx, entry)
	return err
}

// list returns the open tasks, in the order of their start.
func (s *subagents) list() []agent.Task {
	out := make([]agent.Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		out = append(out, t.Task)
	}
	return out
}

// sendTasks sends the open tasks to the carrier.
func (s *subagents) sendTasks() {
	if s.send == nil {
		return
	}
	s.send(agent.EventBackground{Tasks: s.list()})
}
