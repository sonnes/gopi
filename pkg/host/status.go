package host

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
)

// Status is the status of a session.
type Status string

// The statuses of a session. The names follow Claude Managed Agents.
const (
	// StatusIdle: no run is active. The next message runs at once.
	StatusIdle Status = "idle"
	// StatusRunning: a run is active. A message steers it.
	StatusRunning Status = "running"
	// StatusRequiresAction: a tool call of the run waits for
	// [Host.Confirm].
	StatusRequiresAction Status = "requires_action"
)

// StopReason tells why the last run of a session ended.
type StopReason string

// The stop reasons of a run. A failed run is idle with [StopError], so
// a session has three statuses.
const (
	// StopEndTurn: the agent ended the run.
	StopEndTurn StopReason = "end_turn"
	// StopInterrupted: [Host.Interrupt] stopped the run.
	StopInterrupted StopReason = "interrupted"
	// StopError: the run failed. [Session.Error] has the error.
	StopError StopReason = "error"
	// StopRestarted: the host closed during the run, or the program
	// stopped. [Open] sets it, because no agent survives a restart.
	StopRestarted StopReason = "restarted"
)

// Session is a session as the host knows it. The host keeps it in the
// value gopi.host/session and writes it at each change, so the status
// survives a restart.
type Session struct {
	ID     string `json:"id"`
	Status Status `json:"status"`
	// Parent is set for the session of a subagent: the session and the
	// tool call of its parent. The run of the parent writes the child,
	// and the host runs no agent for it, so a child is read-only. Its
	// status follows the subagent events of the parent.
	Parent *agent.ParentRef `json:"parent,omitempty"`
	// Waiting are the tool calls that wait for [Host.Confirm], the
	// oldest first. Two or more calls can wait at the same time. It is
	// set only while Status is [StatusRequiresAction].
	Waiting []agent.ApprovalRequest `json:"waiting,omitempty"`
	// StopReason is why the last run ended. It is set only while Status
	// is [StatusIdle], and is empty before the first run.
	StopReason StopReason `json:"stop_reason,omitempty"`
	// Error is the error of a run that ended with [StopError].
	Error string `json:"error,omitempty"`
	// Usage is the usage of every model call of the last run, summed.
	Usage ai.Usage `json:"usage,omitzero"`
	// Model is the model spec of the session.
	Model string `json:"model,omitempty"`
	// Effort is the thinking level of the config, or empty.
	Effort ai.ThinkingLevel `json:"effort,omitempty"`
	// Mode is the permission mode of the config, or empty.
	Mode agent.Mode `json:"mode,omitempty"`
	// CreatedAt is the time of [Host.Create]. The record of the session
	// in the store has the same time.
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// clone returns a copy of i that shares no slice with i.
func (i Session) clone() Session {
	i.Waiting = slices.Clone(i.Waiting)
	if i.Parent != nil {
		p := *i.Parent
		i.Parent = &p
	}
	return i
}

// The values that the host keeps in each session. The namespace
// "gopi.host" is reserved for the host.
var (
	// sessionValue keeps the [Session].
	sessionValue = session.Value[Session]{
		Namespace: "gopi.host",
		Key:       "session",
	}
	// configValue keeps the [Config] of the session, with the fields of its
	// agent file. Each start of the agent reads it, so a session runs
	// the agent that it started with.
	configValue = session.Value[Config]{
		Namespace: "gopi.host",
		Key:       "config",
	}
)

// now returns the time in UTC. UTC drops the reading of the monotonic
// clock, so a time equals itself after a round trip through a value.
func now() time.Time {
	return time.Now().UTC()
}

// recover returns the session in snap. A session with no value
// gopi.host/session is idle. A session that ran or waited becomes idle
// with [StopRestarted], and recover writes that to the store.
func (h *Host) recover(ctx context.Context, snap session.Snapshot) (Session, error) {
	id := snap.Session.ID
	sess, ok, err := sessionValue.Read(snap)
	if err != nil {
		return Session{}, fmt.Errorf("host: session %s: read the session: %w", id, err)
	}
	config, _, err := configValue.Read(snap)
	if err != nil {
		return Session{}, fmt.Errorf("host: session %s: read the config: %w", id, err)
	}
	sess.ID = id
	sess.CreatedAt = snap.Session.CreatedAt
	parent, isChild, err := agent.Parent.Read(snap)
	if err != nil {
		return Session{}, fmt.Errorf("host: session %s: read the parent: %w", id, err)
	}
	if isChild {
		sess.Parent = &parent
	}
	sess.Model = config.Model
	sess.Mode = config.Mode
	sess.Effort = config.Effort
	stopped := !ok || sess.Status == StatusIdle
	sess.Status = StatusIdle
	if stopped {
		return sess, nil
	}

	sess.StopReason = StopRestarted
	sess.Waiting = nil
	sess.Error = ""
	sess.UpdatedAt = now()
	if err := setStored(ctx, h.store, id, sessionValue, sess); err != nil {
		return Session{}, err
	}
	return sess, nil
}

// setStored writes x as the value v of the session id in the store. It
// is for a session with no agent, because the log of an agent keeps the
// values that it sets.
func setStored[T any](ctx context.Context, store session.Storer, id string, v session.Value[T], x T) error {
	log, err := session.Open(ctx, store, id)
	if err != nil {
		return fmt.Errorf("host: session %s: %w", id, err)
	}
	if err := v.Set(ctx, log, x); err != nil {
		return fmt.Errorf("host: session %s: set %s: %w", id, v.Key, err)
	}
	return nil
}
