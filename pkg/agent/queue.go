package agent

import (
	"sync"

	"github.com/sonnes/gopi/pkg/session"
)

// Steer queues entries that the agent reads before its next model
// call. During a run, the agent drains the queue after the turn in
// progress. Every tool call of that turn runs first. With an entry in
// the queue, the run goes on, also when the turn made no tool calls. A
// Run drains the queue after its input, so a steer before Run goes after
// the input.
//
// Steer does not start a run, because the agent has no goroutine of
// its own and the caller reads the stream of each run. If no run is
// active, call [Agent.Run]. If
// a failure stops the run first, the entries wait for the next Run.
// Steer is safe for concurrent use.
func (a *Agent) Steer(entries ...session.Entry) {
	a.queues.add(&a.queues.steer, entries)
}

// TakeSteer removes the entries of the steer queue that no run read,
// and returns them. A caller that steers a run calls it after the run
// ends: a steer that came after the last read of the run waits in the
// queue, and the caller decides where it goes.
func (a *Agent) TakeSteer() []session.Entry {
	return a.queues.drain(&a.queues.steer)
}

// FollowUp queues entries that the agent reads when the run would
// stop: the turn made no tool calls, and the steer queue is empty. The
// agent drains the queue before the [BeforeStop] hooks run, and the run
// goes on. FollowUp keeps the other rules of [Agent.Steer].
func (a *Agent) FollowUp(entries ...session.Entry) {
	a.queues.add(&a.queues.followUp, entries)
}

// queues are the steer and follow-up queues of an agent. The agent and
// every copy of its state point to the same queues, so a hook queues
// through its copy.
type queues struct {
	mu       sync.Mutex
	steer    []session.Entry
	followUp []session.Entry
}

// add appends entries to the queue q.
func (qs *queues) add(q *[]session.Entry, entries []session.Entry) {
	qs.mu.Lock()
	defer qs.mu.Unlock()
	*q = append(*q, entries...)
}

// drain takes every entry of the queue q. A person who sends three
// corrections wants the model to read all three before its next call.
func (qs *queues) drain(q *[]session.Entry) []session.Entry {
	qs.mu.Lock()
	defer qs.mu.Unlock()
	entries := *q
	*q = nil
	return entries
}
