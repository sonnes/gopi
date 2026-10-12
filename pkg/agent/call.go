package agent

import (
	"context"
	"errors"
	"slices"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
)

// call streams one model response, and emits the message events. The
// log stores the final message before its message_end. If the stream
// fails after it started, call keeps the partial message: see
// keepPartial.
func (a *Agent) call(
	ctx context.Context,
	msgs []ai.Message,
	em *emitter,
) (*ai.Message, error) {
	// The base goes in the options, so a provider sends it in the
	// system prompt and the tool list of its API.
	base, rest := splitBase(msgs)
	opts := ai.JoinOptions(a.cfg.callOpts, baseOptions(base))
	// The clip makes an append of a hook copy the messages, so the
	// messages of the state do not change.
	r := Request{Messages: slices.Clip(rest), Options: opts}
	for _, h := range a.cfg.hooks {
		var err error
		r, err = h.BeforeCall(ctx, a.State(), r)
		if err != nil {
			return nil, err
		}
	}
	s := a.lm.StreamText(ctx, r.Messages, r.Options)

	started := false
	inflight := &ai.Message{Role: ai.RoleAssistant}
	for evt, err := range s.Events() {
		if err != nil {
			return a.keepPartial(ctx, inflight, started, err, em)
		}
		accumulate(inflight, &evt)
		if !started {
			em.emit(EventMessageStart{Message: *snapshot(inflight)})
			started = true
		}
		em.emit(EventMessageUpdate{
			Message:        *snapshot(inflight),
			AssistantEvent: evt,
		})
	}

	final, err := s.Wait()
	if err != nil {
		return a.keepPartial(ctx, inflight, started, err, em)
	}
	if final == nil {
		if started {
			em.emit(EventMessageEnd{Message: ai.Message{Role: ai.RoleAssistant}})
		}
		return nil, errors.New("agent: the provider returned no message")
	}
	if !started {
		// A provider can return a message with no events. The message
		// still gets its start.
		em.emit(EventMessageStart{Message: *final})
	}
	for _, h := range a.cfg.hooks {
		msg, err := h.AfterCall(ctx, a.State(), r, *final)
		if err != nil {
			em.emit(emptyEnd(final.Usage))
			return nil, err
		}
		final = &msg
	}

	if err := a.endMessage(ctx, em, *final); err != nil {
		return nil, err
	}
	return final, nil
}

// endMessage stores the assistant message m, then sends its message_end. If
// the store fails, the message_end carries a message with no content,
// and the run fails.
func (a *Agent) endMessage(ctx context.Context, em *emitter, m ai.Message) error {
	entry := session.NewEntry(m, session.Author(a.cfg.name))
	err := em.commit(ctx, []session.Entry{entry}, EventMessageEnd{Message: m})
	if err != nil {
		em.emit(emptyEnd(m.Usage))
	}
	return err
}

// emptyEnd returns the message_end of a message that does not reach the
// log. The message_start needs its end. A message with no content
// does not reach the state, but the usage of the call counts.
func emptyEnd(usage ai.Usage) EventMessageEnd {
	return EventMessageEnd{
		Message: ai.Message{
			Role:  ai.RoleAssistant,
			Usage: usage,
		},
	}
}

// keepPartial keeps the message that a failure cut short, so the log
// records what the model said, and emits its message_end. It returns
// the message with cause. The stop reason is [ai.StopReasonAborted] if
// ctx is done, and [ai.StopReasonError] if not.
//
// A tool call with no ID did not finish, so keepPartial drops it. If
// nothing is left, the message_end carries a message with no content,
// and the log does not change. If the stream did not start, there is
// no message.
func (a *Agent) keepPartial(
	ctx context.Context,
	inflight *ai.Message,
	started bool,
	cause error,
	em *emitter,
) (*ai.Message, error) {
	if !started {
		return nil, cause
	}
	msg := snapshot(inflight)
	msg.Content = slices.DeleteFunc(msg.Content, unfinishedCall)
	msg.StopReason = ai.StopReasonError
	msg.StopDetail = cause.Error()
	if ctx.Err() != nil {
		msg.StopReason = ai.StopReasonAborted
		msg.StopDetail = ""
	}
	if len(msg.Content) == 0 {
		em.emit(EventMessageEnd{Message: *msg})
		return nil, cause
	}
	if err := a.endMessage(ctx, em, *msg); err != nil {
		return nil, errors.Join(cause, err)
	}
	return msg, cause
}

// unfinishedCall reports if c is a tool call that did not finish. A
// tool call gets its ID when the call ends.
func unfinishedCall(c ai.Content) bool {
	call, ok := c.(ai.ToolCall)
	return ok && call.ID == ""
}

// accumulate applies one model event to the partial message. It grows
// the content blocks as the events arrive.
func accumulate(m *ai.Message, evt *ai.Event) {
	i := evt.ContentIndex
	switch evt.Type {
	case ai.EventTextStart:
		grow(m, i, ai.Text{})
	case ai.EventTextDelta:
		grow(m, i, ai.Text{})
		if t, ok := m.Content[i].(ai.Text); ok {
			t.Text += evt.Delta
			m.Content[i] = t
		}
	case ai.EventTextEnd:
		grow(m, i, ai.Text{})
		if t, ok := m.Content[i].(ai.Text); ok {
			t.Text = evt.Content
			m.Content[i] = t
		}
	case ai.EventThinkStart:
		grow(m, i, ai.ThinkingBlock{})
	case ai.EventThinkDelta:
		grow(m, i, ai.ThinkingBlock{})
		if t, ok := m.Content[i].(ai.ThinkingBlock); ok {
			t.Thinking += evt.Delta
			m.Content[i] = t
		}
	case ai.EventThinkEnd:
		grow(m, i, ai.ThinkingBlock{})
		if t, ok := m.Content[i].(ai.ThinkingBlock); ok {
			t.Thinking = evt.Content
			m.Content[i] = t
		}
	case ai.EventToolStart:
		grow(m, i, ai.ToolCall{})
	case ai.EventToolEnd:
		if evt.ToolCall != nil {
			grow(m, i, ai.ToolCall{})
			m.Content[i] = *evt.ToolCall
		}
	}
}

// grow extends the content of m until index i exists.
func grow(m *ai.Message, i int, zero ai.Content) {
	for len(m.Content) <= i {
		m.Content = append(m.Content, zero)
	}
}

// snapshot returns a copy of m with its own content slice, so later
// deltas do not change an event that was already sent.
func snapshot(m *ai.Message) *ai.Message {
	cp := *m
	cp.Content = make([]ai.Content, len(m.Content))
	copy(cp.Content, m.Content)
	return &cp
}
