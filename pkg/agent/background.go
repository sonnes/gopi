package agent

import "time"

// TaskKind names the kind of a background [Task].
type TaskKind string

// The kinds of a background task.
const (
	// TaskCommand is a shell command in the background, such as a build.
	TaskCommand TaskKind = "command"
	// TaskMonitor is a command whose lines wake the agent, such as a
	// watch of a log.
	TaskMonitor TaskKind = "monitor"
	// TaskWakeup is a turn that the agent scheduled for a later time.
	TaskWakeup TaskKind = "wakeup"
)

// Task is one piece of background work of an agent that outlives the
// run that started it. A subagent in the background is not a Task: it
// has its own subagent events.
type Task struct {
	// ID is the ID that the agent gives the task, such as the task ID of
	// the CLI, or the ID of the tool call of a wakeup.
	ID   string   `json:"id"`
	Kind TaskKind `json:"kind"`
	// Title is a short title for a UI, such as the description of the
	// command. It is "" if the agent knows none.
	Title     string    `json:"title,omitempty"`
	StartedAt time.Time `json:"started_at"`
	// Due is the time of a [TaskWakeup]. It is zero for the other kinds.
	Due time.Time `json:"due,omitzero"`
}

// EventBackground gives the open background tasks of the agent after
// each change, in the order of their start. An empty list means that no
// task is open. The agent sends it in a run, or between runs to the
// Idle function of its [Config]. [State.Apply] ignores it.
type EventBackground struct {
	Tasks []Task
}

func (EventBackground) event() {}

// MarshalJSON encodes the event with its type.
func (e EventBackground) MarshalJSON() ([]byte, error) { return marshalEvent(e) }

// KindStopped is the custom entry that an agent adds to its log when a
// close stops background tasks that did not end. Its data is a
// [Stopped]. A viewer of the log shows that the work did not finish.
const KindStopped = "gopi.stopped"

// Stopped is the data of a [KindStopped] entry.
type Stopped struct {
	Tasks []Task `json:"tasks"`
}
