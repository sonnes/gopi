package cliagent

import (
	"context"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
)

// Child is the session of one subagent of the CLI, and the emitter of
// its runs.
type Child struct {
	// SessionID is the ID of the child session. It is "" if the parent
	// is unsaved.
	SessionID string
	Log       *session.Log
	Emitter   *Emitter
}

// OpenChild opens the child session of the tool call callID under the
// session parentID. The ID of the child comes from [agent.ChildID] with
// name and suffix. A new log gets the [agent.Parent] of the child. A
// child that exists, such as one that a replay finds again, keeps its
// log. If parentID is "" or the agent is unsaved, the child gets an
// unsaved log.
//
// The emitter of the child sends each event to send, in an
// [agent.EventSubagent]. The writes ignore the cancel of ctx, because
// the CLI goes on with the child after an interrupt.
func (c *Core) OpenChild(
	ctx context.Context,
	parentID, name, suffix, callID string,
	send func(agent.Event),
) (*Child, error) {
	id := agent.ChildID(parentID, name, suffix)
	ref := agent.ParentRef{
		SessionID:  parentID,
		ToolCallID: callID,
	}
	log, err := openLog(context.WithoutCancel(ctx), c.store, id, ref)
	if err != nil {
		return nil, err
	}
	push := func(ev agent.Event) {
		send(agent.EventSubagent{
			ToolCallID: callID,
			SessionID:  id,
			Event:      ev,
		})
	}
	return &Child{
		SessionID: id,
		Log:       log,
		Emitter:   NewEmitter(log, NewState(log), push),
	}, nil
}

// openLog returns the log of the child session id in store, and writes
// ref as its [agent.Parent] when the log is new.
func openLog(ctx context.Context, store session.Storer, id string, ref agent.ParentRef) (*session.Log, error) {
	if id == "" || store == nil {
		return session.New(), nil
	}
	log, err := session.Open(ctx, store, id)
	if err != nil {
		return nil, err
	}
	if len(log.Entries()) > 0 {
		return log, nil
	}
	if err := agent.Parent.Set(ctx, log.Writer(), ref); err != nil {
		return nil, err
	}
	return log, nil
}

// EndText returns the Text of the [agent.EventSubagentEnd] of a child
// with status: the error err of a failed child, or else the last text
// of msgs.
func EndText(msgs []ai.Message, status string, err error) string {
	if status == agent.SubagentFailed && err != nil {
		return err.Error()
	}
	return agent.LastText(msgs)
}
