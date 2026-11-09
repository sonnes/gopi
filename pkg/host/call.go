package host

import (
	"context"
	"fmt"
	"slices"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
)

// Call runs m once on the driver of config.Model, and returns the last
// assistant message of the run. The message has the usage of its model
// call. Call keeps no session: the agent writes to [session.Discard], it
// is not in [Host.List] or [Host.Events], and it stops before Call
// returns. Give the instructions of the call in m.
//
// Call reads Model, Effort, Mode, Dir, AddDirs, ToolNames, and Deny of
// config. It does not read the other fields. Nobody can answer a tool
// call that asks for an approval, so the agent gets a denial.
//
// A config that [Host.Create] refuses fails with [ErrInvalid]. If ctx
// ends, the run stops, and Call returns the error of ctx. Close
// stops each call, and waits for it. After Close, Call returns
// [ErrClosed]. If the run has no assistant message, Call returns
// [ErrInvalid].
func (h *Host) Call(ctx context.Context, config Config, m Message) (ai.Message, error) {
	ctx, done, err := h.startCall(ctx)
	if err != nil {
		return ai.Message{}, err
	}
	defer done()

	if _, err := h.check(config, false); err != nil {
		return ai.Message{}, fmt.Errorf("host: call: %w", err)
	}
	d, model, err := h.cat.Driver(config.Model)
	if err != nil {
		return ai.Message{}, fmt.Errorf("host: call: %w", err)
	}
	runner, err := d.Start(ctx, agent.Config{
		SessionID: h.newID(),
		Store:     session.Discard{},
		Spec:      config.Model,
		Model:     model,
		Effort:    config.Effort,
		Mode:      config.Mode,
		Dir:       config.Dir,
		AddDirs:   config.AddDirs,
		ToolNames: config.ToolNames,
		Deny:      config.Deny,
		Tools:     h.tools,
		Approve:   deny,
	})
	if err != nil {
		return ai.Message{}, fmt.Errorf("host: call: %w", err)
	}
	defer func() { _ = runner.Close() }()

	msgs, err := runner.Run(ctx, m...).Wait()
	if err != nil {
		return ai.Message{}, fmt.Errorf("host: call: %w", err)
	}
	return lastAssistant(msgs)
}

// startCall adds a call to the host. It returns a context that ends with
// ctx or with Close, and a function that ends the call. After Close, it
// returns [ErrClosed].
func (h *Host) startCall(ctx context.Context) (context.Context, func(), error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, nil, fmt.Errorf("host: call: %w", ErrClosed)
	}
	h.calls.Add(1)

	ctx, cancel := context.WithCancelCause(ctx)
	stop := context.AfterFunc(h.callsCtx, func() { cancel(ErrClosed) })
	done := func() {
		stop()
		cancel(nil)
		h.calls.Done()
	}
	return ctx, done, nil
}

// deny answers each approval of a call with a denial.
func deny(context.Context, agent.ApprovalRequest) (agent.Decision, error) {
	return agent.Decision{}, nil
}

// lastAssistant returns the last assistant message of msgs. If msgs has
// none, it returns an error that wraps [ErrInvalid].
func lastAssistant(msgs []ai.Message) (ai.Message, error) {
	for _, m := range slices.Backward(msgs) {
		if m.Role == ai.RoleAssistant {
			return m, nil
		}
	}
	return ai.Message{}, fmt.Errorf("host: call: no reply: %w", ErrInvalid)
}
