package agent

import "github.com/sonnes/gopi/pkg/ai"

// Steer queues messages that the agent reads before its next model
// call. During a run, the agent drains the queue after the turn in
// progress. Every tool call of that turn runs first. With a message in
// the queue, the run goes on, also when the turn made no tool calls. A
// Run drains the queue after its input, so a steer before Run goes after
// the input.
//
// Steer does not start a run. If no run is active, call [Agent.Run]. If
// a failure stops the run first, the messages wait for the next Run.
// Steer is safe for concurrent use.
func (a *Agent) Steer(msgs ...ai.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.steer = append(a.state.steer, msgs...)
}

// FollowUp queues messages that the agent reads when the run would
// stop: the turn made no tool calls, and the steer queue is empty. The
// agent drains the queue before the [BeforeStop] hooks run, and the run
// goes on. FollowUp keeps the other rules of [Agent.Steer].
func (a *Agent) FollowUp(msgs ...ai.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.followUp = append(a.state.followUp, msgs...)
}

// drain takes every message of the queue q.
func (a *Agent) drain(q *[]ai.Message) []ai.Message {
	a.mu.Lock()
	defer a.mu.Unlock()
	msgs := *q
	*q = nil
	return msgs
}
