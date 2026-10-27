package claude

import (
	"context"
	"fmt"
	"slices"

	"github.com/sonnes/gopi/internal/transcript"
	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/internal/emit"
	"github.com/sonnes/gopi/pkg/agent/internal/sections"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
	"github.com/sonnes/gopi/pkg/stream"
)

// The CLI keeps its background tasks: a subagent or a shell command can
// go on after the result of the run that started it. Between runs, an
// idle reader reads the lines of the CLI:
//
//	result            the run ends; a child in the background goes on
//	user, assistant   lines of the child  -> its log, and the Idle events
//	task_notification the child ends      -> the Idle events
//	system init       the CLI starts a turn by itself -> kept, and Wake
//	assistant, result the lines of that turn -> kept
//
// Continue runs that turn: it applies the kept lines, then reads the CLI
// until the result. A run with input that finds kept lines applies them
// first, and their result ends only a segment.

// idleReader is the reader of the lines of the CLI between runs.
type idleReader struct {
	stop chan struct{}
	done chan struct{}
}

// Continue implements [agent.Waker]. It runs the turn that the CLI
// started between runs: it sends no input, and it ends at the result of
// that turn. The log gets an [agent.KindWake] entry first. If a run with
// input already read the turn, Continue ends at once, with no entry.
func (a *Agent) Continue(ctx context.Context) *agent.Stream {
	return stream.New(func(push func(agent.Event)) ([]ai.Message, error) {
		return a.runWith(ctx, push, nil)
	})
}

// runWith is the producer of [Agent.Run] and of [Agent.Continue]. It
// stops the idle reader, runs, and starts the idle reader again if the
// CLI goes on.
func (a *Agent) runWith(ctx context.Context, push func(agent.Event), entries []session.Entry) ([]ai.Message, error) {
	em := emit.New(a.log, a.name, a.state, push)
	if err := em.Start(); err != nil {
		return nil, fmt.Errorf("claude: %w", err)
	}
	// A run with input gets the sections. The turn of Continue has no
	// input to carry a change.
	var change *ai.Message
	if entries != nil {
		base, c, err := sections.ForCLI(ctx, a.cfg.sections, a.log.Messages())
		if err != nil {
			return em.Finish(ctx, ai.Usage{}, fmt.Errorf("claude: %w", err))
		}
		// The base goes in the log before the CLI starts, because the
		// CLI gets it as its system prompt.
		if base != nil {
			if err := em.Input(ctx, []session.Entry{session.NewEntry(*base)}); err != nil {
				return em.Finish(ctx, ai.Usage{}, err)
			}
		}
		change = c
	}
	kept := a.pauseIdle()
	// Start the CLI before the input, so that a failed start stores
	// no input that the CLI never got.
	proc, err := a.ensure(ctx)
	if err != nil {
		a.endBackground()
		return em.Finish(ctx, ai.Usage{}, err)
	}
	// The error of a hook belongs to the run that called it.
	proc.hookErr = nil
	r := newRunner(a, proc, em)
	if change != nil {
		r.reminder = transcript.Reminder(transcript.SystemText(change.Content))
		entries = append([]session.Entry{session.NewEntry(*change)}, entries...)
	}
	a.subs.carry(em.Subagent)
	msgs, err := r.run(ctx, entries, kept)
	// The CLI did not take these steers before the run ended. It
	// must not run them after the run, so the agent stops it, and
	// the host sends them again.
	if lost := r.lostSteers(); len(lost) > 0 {
		a.giveBack(lost)
		a.drop(proc)
	}
	a.resumeIdle(proc)
	return msgs, err
}

// resumeIdle starts the idle reader on proc, if proc is still the CLI
// of the agent.
func (a *Agent) resumeIdle(proc *process) {
	ir := &idleReader{
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
	a.mu.Lock()
	alive := a.proc == proc
	if alive {
		a.idle = ir
	}
	a.mu.Unlock()
	if !alive {
		a.endBackground()
		return
	}
	go a.readIdle(proc, ir)
}

// pauseIdle stops the idle reader, and returns the lines that it kept.
// The reader owns the tracker and the kept lines until it is done, so
// they need no lock.
func (a *Agent) pauseIdle() []*line {
	a.mu.Lock()
	ir := a.idle
	a.idle = nil
	a.mu.Unlock()
	if ir != nil {
		close(ir.stop)
		<-ir.done
	}
	kept := a.kept
	a.kept = nil
	return kept
}

// readIdle reads the lines of proc until a run stops it, or until the
// CLI exits. The children in the background go on, with their events on
// the Idle function of the agent.
func (a *Agent) readIdle(proc *process, ir *idleReader) {
	defer close(ir.done)
	ctx := context.Background()
	a.subs.carry(a.sendIdle)
	woke := false
	for {
		select {
		case <-ir.stop:
			a.releaseIdle(ctx, true)
			return
		case data, ok := <-proc.lines():
			if !ok {
				a.releaseIdle(ctx, false)
				return
			}
			l := parseLine(data)
			if l == nil {
				continue
			}
			if a.idleLine(ctx, l) && !woke {
				woke = true
				a.signalWake()
			}
		}
	}
}

// releaseIdle gives back the children at the end of the idle reader.
// No run reads the error: each child that ends carries the error of its
// log in its end.
func (a *Agent) releaseIdle(ctx context.Context, keep bool) {
	_ = a.subs.release(ctx, keep)
}

// idleLine applies one line between runs. A line of a child goes to the
// child. A line of the parent is kept for the next run. It reports
// whether the line starts a turn of the parent. An error ends the child
// of the line as failed.
func (a *Agent) idleLine(ctx context.Context, l *line) bool {
	s := a.subs
	if l.ParentToolUseID != nil {
		id := *l.ParentToolUseID
		// A child that a kept turn started opens when a run applies
		// that turn, so its lines are kept with the turn.
		if _, open := s.open[id]; !open {
			a.kept = append(a.kept, l)
			return false
		}
		s.fail(ctx, id, s.line(ctx, id, l))
		return false
	}
	_, open := s.open[l.ToolUseID]
	ends := l.Type == "system" && l.Subtype == "task_notification" && open
	if ends {
		s.fail(ctx, l.ToolUseID, s.taskEnded(ctx, l))
		return false
	}
	a.kept = append(a.kept, l)
	return rootLine(l)
}

// rootLine reports whether l is a line of a turn of the parent: the
// start of a turn, a message of the parent, a request of the CLI, or a
// result.
func rootLine(l *line) bool {
	if l.ParentToolUseID != nil {
		return false
	}
	switch l.Type {
	case "assistant", "stream_event", "result", "control_request":
		return true
	case "user":
		return !l.IsReplay
	case "system":
		return l.Subtype == "init"
	}
	return false
}

// turnOpen reports whether lines leave a turn of the parent open: a turn
// starts, and its result is not in lines. A request of the CLI is not a
// turn, because a child in the background can ask for a permission.
func turnOpen(lines []*line) bool {
	open := false
	for _, l := range lines {
		if rootLine(l) && l.Type != "control_request" {
			open = l.Type != "result"
		}
	}
	return open
}

// requeue puts lines that a run did not apply before the kept lines,
// for the next run. If they start a turn, the agent calls Wake.
func (a *Agent) requeue(lines []*line) {
	if len(lines) == 0 {
		return
	}
	a.kept = append(slices.Clone(lines), a.kept...)
	if slices.ContainsFunc(lines, rootLine) {
		a.signalWake()
	}
}

// signalWake calls the Wake function, if the agent has one.
func (a *Agent) signalWake() {
	if a.cfg.wake != nil {
		a.cfg.wake()
	}
}

// sendIdle sends an event between runs to the Idle function.
func (a *Agent) sendIdle(ev agent.Event) {
	if a.cfg.idle != nil {
		a.cfg.idle(ev)
	}
}

// endBackground ends each child in the background as interrupted, on
// the Idle function: the CLI that ran them is gone.
func (a *Agent) endBackground() {
	a.subs.carry(a.sendIdle)
	a.releaseIdle(context.Background(), false)
}

// sessionID returns the ID of the session of a, or "" if it is unsaved.
func (a *Agent) sessionID() string {
	if a.store == nil {
		return ""
	}
	return a.log.Session().ID
}
