package claude

import (
	"context"
	"crypto/rand"
	"fmt"
	"slices"

	"github.com/sonnes/gopi/pkg/agent/internal/emit"
	"github.com/sonnes/gopi/pkg/session"
)

// steerPriority is the priority of a steer. The CLI takes a user line
// with it before its next model call: it ends the segment that runs with
// a result line, and starts a new segment with the line. A tool that
// runs in the foreground ends first.
const steerPriority = "now"

// subagentTools are the tools that start a subagent. A steer cancels a
// subagent that runs in the foreground, so the agent moves it to the
// background first, and the subagent ends its work.
var subagentTools = []string{"Agent", "Task"}

// newUUID returns the ID of a steer. The CLI echoes the ID when it takes
// the steer, so the log keeps the steer at that point.
var newUUID = func() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// pendingSteer is a steer that the runner sent and that the CLI did not
// take yet.
type pendingSteer struct {
	uuid    string
	entries []session.Entry
}

// Steer gives entries to the active run. The run sends their text and
// files to the CLI at once, and the log keeps the entries where the CLI
// takes them. With no active run, Steer keeps the entries for
// [Agent.TakeSteer]. Steer is safe for concurrent use.
func (a *Agent) Steer(entries ...session.Entry) {
	a.steerMu.Lock()
	a.steer = append(a.steer, entries...)
	a.steerMu.Unlock()

	select {
	case a.wake <- struct{}{}:
	default:
	}
}

// TakeSteer removes the entries of the steers that no run sent, and of
// the steers that the CLI did not take before its run ended. A host runs
// them next.
func (a *Agent) TakeSteer() []session.Entry {
	a.steerMu.Lock()
	defer a.steerMu.Unlock()

	out := a.steer
	a.steer = nil
	return out
}

// giveBack puts entries before the waiting steers, for TakeSteer.
func (a *Agent) giveBack(entries []session.Entry) {
	a.steerMu.Lock()
	defer a.steerMu.Unlock()

	a.steer = append(slices.Clone(entries), a.steer...)
}

// sendSteer sends the waiting steers to the CLI. It first moves each
// subagent that runs to the background. After an interrupt, it sends
// nothing, and the steers wait for TakeSteer.
func (r *runner) sendSteer(ctx context.Context) error {
	if r.interrupted {
		return nil
	}
	entries := r.a.TakeSteer()
	if len(entries) == 0 {
		return nil
	}
	content := blocks(emit.Input(entries))
	if len(content) == 0 {
		// A steer with no text and no file has nothing for the model,
		// so the log keeps it at once.
		return r.em.Input(ctx, entries)
	}

	for _, id := range r.openSubagents() {
		body := outRequestBody{
			Subtype:   "background_tasks",
			ToolUseID: id,
		}
		if _, err := r.proc.request(body); err != nil {
			return err
		}
	}

	id := newUUID()
	err := r.proc.send(outUser{
		Type: "user",
		Message: outUserMessage{
			Role:    "user",
			Content: content,
		},
		Priority: steerPriority,
		UUID:     id,
	})
	if err != nil {
		return err
	}
	r.pending = append(r.pending, pendingSteer{
		uuid:    id,
		entries: entries,
	})
	r.steered = true
	return nil
}

// replayed stores the steer whose ID the CLI echoed, at the point where
// the CLI takes it. The echo of other input stores nothing.
func (r *runner) replayed(ctx context.Context, l *line) error {
	i := slices.IndexFunc(r.pending, func(p pendingSteer) bool { return p.uuid == l.UUID })
	if i < 0 {
		return nil
	}
	p := r.pending[i]
	r.pending = slices.Delete(r.pending, i, i+1)
	if err := r.finish(ctx); err != nil {
		return err
	}
	return r.em.Input(ctx, p.entries)
}

// abortedReasons are the terminal reasons of a segment that a steer cut.
var abortedReasons = []string{"aborted_tools", "aborted_streaming"}

// segmentEnded reports whether a result line ends only a segment of the
// run: the CLI has a steer to take, so a new segment follows. An
// interrupt ends the run.
func (r *runner) segmentEnded(l *line) bool {
	if r.interrupted {
		return false
	}
	cutBySteer := r.steered && slices.Contains(abortedReasons, l.TerminalReason)
	return len(r.pending) > 0 || cutBySteer
}

// openSubagents returns the IDs of the subagent calls that wait for
// their result, in order.
func (r *runner) openSubagents() []string {
	var ids []string
	for id := range r.subagents {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// lostSteers returns the entries of the steers that the CLI did not take.
func (r *runner) lostSteers() []session.Entry {
	var out []session.Entry
	for _, p := range r.pending {
		out = append(out, p.entries...)
	}
	return out
}
