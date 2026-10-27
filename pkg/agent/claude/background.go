package claude

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
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
//	system init       the CLI starts a turn by itself -> kept, and Wakes
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

// Wakes implements [agent.Waker]. It sends one value when the CLI starts
// a turn between runs.
func (a *Agent) Wakes() <-chan struct{} { return a.wakes }

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
		a.endBackground(agent.SubagentInterrupted)
		return em.Finish(ctx, ai.Usage{}, err)
	}
	// The error of a hook belongs to the run that called it.
	proc.hookErr = nil
	r := newRunner(a, proc, em)
	if change != nil {
		r.reminder = transcript.Reminder(transcript.SystemText(change.Content))
		entries = append([]session.Entry{session.NewEntry(*change)}, entries...)
	}
	if err := r.adoptBackground(ctx); err != nil {
		return r.fail(ctx, err)
	}
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

// adoptBackground carries each child in the background in the run of r:
// it sends a new start of the child, and its lines go to the run.
func (r *runner) adoptBackground(ctx context.Context) error {
	var errs []error
	for _, c := range r.a.takeBackground() {
		errs = append(errs, r.attach(ctx, c, r, r.em.Subagent, false))
	}
	return errors.Join(errs...)
}

// resumeIdle starts the idle reader on proc, if proc is still the CLI
// of the agent.
func (a *Agent) resumeIdle(proc *process) {
	a.mu.Lock()
	alive := a.proc == proc
	a.mu.Unlock()
	if !alive {
		a.endBackground(agent.SubagentInterrupted)
		return
	}
	ir := &idleReader{
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
	a.idleMu.Lock()
	a.idle = ir
	a.idleMu.Unlock()
	r := &runner{
		a:         a,
		proc:      proc,
		sessionID: a.sessionID(),
		subagents: map[string]bool{},
		tasks:     newTasks(),
	}
	go a.readIdle(r, ir)
}

// pauseIdle stops the idle reader, and returns the lines that it kept.
// A wake that no run took goes too, because the run now reads the turn.
func (a *Agent) pauseIdle() [][]byte {
	a.idleMu.Lock()
	ir := a.idle
	a.idle = nil
	a.idleMu.Unlock()
	if ir != nil {
		close(ir.stop)
		<-ir.done
	}
	a.idleMu.Lock()
	kept := a.kept
	a.kept = nil
	a.idleMu.Unlock()
	select {
	case <-a.wakes:
	default:
	}
	return kept
}

// readIdle reads the lines of the CLI until a run stops it, or until the
// CLI exits. The children in the background go on, with their events on
// the Idle function of the agent.
func (a *Agent) readIdle(r *runner, ir *idleReader) {
	defer close(ir.done)
	ctx := context.Background()
	for _, c := range a.takeBackground() {
		_ = r.attach(ctx, c, r, a.sendIdle, false)
	}
	woke := false
	for {
		select {
		case <-ir.stop:
			_ = r.endChildren(ctx, true)
			return
		case data, ok := <-r.proc.lines():
			if !ok {
				_ = r.endChildren(ctx, false)
				return
			}
			starts := a.idleLine(ctx, r, data)
			if starts && !woke {
				woke = true
				select {
				case a.wakes <- struct{}{}:
				default:
				}
			}
		}
	}
}

// idleLine applies one line between runs. A line of a child goes to the
// child. A line of the parent is kept for the next run. It reports
// whether the line starts a turn of the parent.
func (a *Agent) idleLine(ctx context.Context, r *runner, data []byte) bool {
	var l line
	if err := json.Unmarshal(data, &l); err != nil {
		return false
	}
	if l.ParentToolUseID != nil {
		// A child that a kept turn started opens when a run applies
		// that turn, so its lines are kept with the turn.
		if _, open := r.tasks.open[*l.ParentToolUseID]; !open {
			a.keepLine(data)
			return false
		}
		_ = r.childLine(ctx, *l.ParentToolUseID, &l)
		return false
	}
	if l.Type == "system" {
		_, isCall := r.tasks.calls[l.ToolUseID]
		_, isOpen := r.tasks.open[l.ToolUseID]
		switch {
		case l.Subtype == "task_started" && isCall:
			_ = r.taskStarted(ctx, &l)
			return false
		case l.Subtype == "task_notification" && isOpen:
			_ = r.taskEnded(ctx, &l)
			return false
		}
	}
	a.keepLine(data)
	return rootLine(&l)
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

// rootData reports whether the line data is a line of a turn of the
// parent.
func rootData(data []byte) bool {
	var l line
	if err := json.Unmarshal(data, &l); err != nil {
		return false
	}
	return rootLine(&l)
}

// requeue puts lines that a run did not apply before the kept lines,
// for the next run. If they start a turn, Wakes sends a value.
func (a *Agent) requeue(lines [][]byte) {
	if len(lines) == 0 {
		return
	}
	a.idleMu.Lock()
	a.kept = append(slices.Clone(lines), a.kept...)
	a.idleMu.Unlock()
	if slices.ContainsFunc(lines, rootData) {
		select {
		case a.wakes <- struct{}{}:
		default:
		}
	}
}

// keepLine keeps a line of the parent for the next run.
func (a *Agent) keepLine(data []byte) {
	a.idleMu.Lock()
	defer a.idleMu.Unlock()
	a.kept = append(a.kept, data)
}

// sendIdle sends an event between runs to the Idle function.
func (a *Agent) sendIdle(ev agent.Event) {
	if a.cfg.idle != nil {
		a.cfg.idle(ev)
	}
}

// keep gives a child in the background to the agent, for the next run.
func (a *Agent) keep(c *child) {
	a.idleMu.Lock()
	defer a.idleMu.Unlock()
	a.background[c.callID] = c
}

// forget removes a child that ended.
func (a *Agent) forget(callID string) {
	a.idleMu.Lock()
	defer a.idleMu.Unlock()
	delete(a.background, callID)
}

// takeBackground removes and returns the children in the background, by
// the ID of their call.
func (a *Agent) takeBackground() []*child {
	a.idleMu.Lock()
	defer a.idleMu.Unlock()
	out := slices.Collect(maps.Values(a.background))
	clear(a.background)
	slices.SortFunc(out, func(x, y *child) int { return cmp.Compare(x.callID, y.callID) })
	return out
}

// endBackground ends each child in the background with status, on the
// Idle function: the CLI that ran them is gone.
func (a *Agent) endBackground(status string) {
	for _, c := range a.takeBackground() {
		a.sendIdle(agent.EventSubagentStart{
			ToolCallID: c.callID,
			SessionID:  c.sessionID,
			Name:       c.name,
			Title:      c.title,
			Prompt:     c.prompt,
			Model:      c.model,
		})
		a.sendIdle(agent.EventSubagentEnd{
			ToolCallID: c.callID,
			SessionID:  c.sessionID,
			Status:     status,
		})
	}
}

// sessionID returns the ID of the session of a, or "" if it is unsaved.
func (a *Agent) sessionID() string {
	if a.store == nil {
		return ""
	}
	return a.log.Session().ID
}
