package host

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
)

// approveFunc returns the approve function that the host gives the
// agent. With no approve function in the spec, it returns nil, and the
// agent denies each call that needs a person.
func (s *Session) approveFunc() agent.ApproveFunc {
	if s.spec.Approve == nil {
		return nil
	}
	return s.approve
}

// approval is one call that waits for the approve function.
type approval struct {
	callID string
}

// errRunEnded is the error of an approval that its run did not wait
// for.
var errRunEnded = errors.New("host: the run ended")

// approve passes call to the approve function of the spec. The host
// stream gets [EventApprovalStart] before the call, and
// [EventApprovalEnd] after it. Two calls can wait at the same time.
//
// If no run is active, approve returns [ErrNoRun]. If the session
// closes, it returns [ErrClosed]. In both cases the stream gets no
// event. The context of the call also ends when the active run ends.
// The Codex agent calls approve with the context of its connection, so
// without this an interrupt would leave the call waiting.
//
// The Codex agent also does not wait for approve. So the end of the
// run publishes the approval_end of each call that waits, before
// run_end, and a later answer publishes nothing.
func (s *Session) approve(ctx context.Context, call ai.ToolCall) (agent.Decision, error) {
	s.mu.Lock()
	runCtx := s.runCtx
	if s.closing {
		s.mu.Unlock()
		return agent.Decision{}, fmt.Errorf("host: session %s: %w", s.id, ErrClosed)
	}
	if runCtx == nil {
		s.mu.Unlock()
		return agent.Decision{}, fmt.Errorf("host: session %s: %w", s.id, ErrNoRun)
	}
	a := &approval{callID: call.ID}
	s.pending = append(s.pending, a)
	s.publishLocked(EventApprovalStart{
		Session: s.id,
		Call:    call,
	})
	s.mu.Unlock()

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stop := context.AfterFunc(runCtx, func() {
		cancel(context.Cause(runCtx))
	})
	defer stop()
	d, err := s.spec.Approve(ctx, call)

	end := EventApprovalEnd{
		Session:    s.id,
		ToolCallID: call.ID,
		Decision:   d,
	}
	// A call that the end of its context stopped reports the cause, such
	// as ErrInterrupted, and not context.Canceled.
	cause := context.Cause(ctx)
	switch {
	case err == nil:
	case cause != nil && errors.Is(err, context.Canceled):
		end.Decision = agent.Decision{}
		end.Error = cause.Error()
	default:
		end.Decision = agent.Decision{}
		end.Error = err.Error()
	}
	s.mu.Lock()
	if s.takePendingLocked(a) {
		s.publishLocked(end)
	}
	s.mu.Unlock()
	return d, err
}

// takePendingLocked removes a from the approvals that wait. It reports
// false if the end of the run removed a first.
func (s *Session) takePendingLocked(a *approval) bool {
	i := slices.Index(s.pending, a)
	if i < 0 {
		return false
	}
	s.pending = slices.Delete(s.pending, i, i+1)
	return true
}

// endPendingLocked publishes the approval_end of each approval that
// waits, with the cause of the end of the run, or with errRunEnded.
func (s *Session) endPendingLocked(cause error) {
	if cause == nil {
		cause = errRunEnded
	}
	for _, a := range s.pending {
		s.publishLocked(EventApprovalEnd{
			Session:    s.id,
			ToolCallID: a.callID,
			Error:      cause.Error(),
		})
	}
	s.pending = nil
}
