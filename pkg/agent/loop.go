package agent

import (
	"context"

	"github.com/sonnes/gopi/internal/transcript"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
)

// run is the producer behind [Agent.Run]. It returns the messages that
// it produced, with the error if the run failed. The input is not in
// the result, because the caller already has it. The order of events:
//
//	agent_start
//	turn_start
//	  message_start, message_end...                     (interrupted results, turn 1 only)
//	  message_start, message_end...                     (system change, if any)
//	  message_start, message_end...                     (new messages)
//	  message_start, message_update..., message_end     (assistant)
//	  tool_execution_start...                           (each call, in order)
//	  tool_execution_end, message_start, message_end... (each call and its result, as the call ends)
//
// The result of a call comes right after its end. With calls in order,
// each call emits its start, its end, and its result before the next
// call starts.
//
//	turn_end
//	message_start, message_end...                       (AfterTurn entries)
//	... the next turn, while the model calls tools or a message continues the run ...
//	agent_end
//
// The interrupted results answer the tool calls that an earlier run
// left with no result. The new messages of the first turn are the
// input, then the steer queue. The new messages of a later turn are the
// steer queue. If the turn before made no tool calls and the steer
// queue was empty, they are the follow-up queue. If that was empty too,
// they are the entries of the BeforeStop hooks.
//
// The log stores each message before its message_end. An ephemeral
// message gets its events, and the store does not keep it. A custom
// entry is stored with no event.
//
// [Agent.Run] sends agent_start before run. agent_end comes on every
// exit, with the error of a run that failed.
func (a *Agent) run(
	ctx context.Context,
	input []session.Entry,
	em *emitter,
) (produced []ai.Message, err error) {
	defer func() {
		end := EventAgentEnd{Usage: a.State().Usage()}
		if err != nil {
			end.Error = err.Error()
		}
		em.emit(end)
	}()

	next := a.queues.drain(&a.queues.steer)
	for turn := 1; ; turn++ {
		out, calledTools, err := a.turn(ctx, turn, input, next, em)
		produced = append(produced, out...)
		if err != nil {
			return produced, err
		}
		input = nil

		added, err := a.afterTurn(ctx, em)
		produced = append(produced, added...)
		if err != nil {
			return produced, err
		}

		next, err = a.next(ctx, calledTools)
		if err != nil {
			return produced, err
		}
		stop := !calledTools && len(next) == 0
		if stop {
			return produced, nil
		}
	}
}

// afterTurn runs the AfterTurn hooks on the turn that ended. After
// each hook, it adds the entries of the hook, so the next hook sees
// them in its state. It returns the messages that it added.
func (a *Agent) afterTurn(ctx context.Context, em *emitter) ([]ai.Message, error) {
	var added []ai.Message
	for _, h := range a.cfg.hooks {
		at, ok := h.(AfterTurnHook)
		if !ok {
			continue
		}
		entries, err := at.AfterTurn(ctx, a.State())
		if err != nil {
			return added, err
		}
		msgs, err := a.add(ctx, em, entries...)
		added = append(added, msgs...)
		if err != nil {
			return added, err
		}
	}
	return added, nil
}

// next returns the entries that the agent reads in the next turn. It
// drains the steer queue. If the queue was empty and the turn made no
// tool calls, the run would stop: next then drains the follow-up queue,
// and if that was empty too, runs the BeforeStop hooks. With no
// entries and no tool calls, the run stops.
func (a *Agent) next(ctx context.Context, calledTools bool) ([]session.Entry, error) {
	if steer := a.queues.drain(&a.queues.steer); len(steer) > 0 {
		return steer, nil
	}
	if calledTools {
		return nil, nil
	}
	if followUp := a.queues.drain(&a.queues.followUp); len(followUp) > 0 {
		return followUp, nil
	}
	return a.beforeStop(ctx)
}

// beforeStop runs the BeforeStop hooks until one continues the run. It
// returns the entries of that hook, or nil if the run stops.
func (a *Agent) beforeStop(ctx context.Context) ([]session.Entry, error) {
	for _, h := range a.cfg.hooks {
		bs, ok := h.(BeforeStopHook)
		if !ok {
			continue
		}
		entries, err := bs.BeforeStop(ctx, a.State())
		if err != nil {
			return nil, err
		}
		if len(entries) > 0 {
			return entries, nil
		}
	}
	return nil, nil
}

// add stores entries in the log, one at a time, and returns their
// messages. Each message gets message_start and message_end after the
// store, so a consumer sees it as a message, and the log has it. A
// custom entry gets no event. If the store fails, the entry gets no
// event, and add stops.
func (a *Agent) add(ctx context.Context, em *emitter, entries ...session.Entry) ([]ai.Message, error) {
	var msgs []ai.Message
	for _, e := range entries {
		if e.Kind != "" {
			if err := em.commit(ctx, []session.Entry{e}); err != nil {
				return msgs, err
			}
			continue
		}
		end := EventMessageEnd{
			Message:   e.Message,
			Ephemeral: e.Ephemeral,
		}
		err := em.commit(ctx,
			[]session.Entry{e},
			EventMessageStart{Message: e.Message},
			end,
		)
		if err != nil {
			return msgs, err
		}
		msgs = append(msgs, e.Message)
	}
	return msgs, nil
}

// interrupted returns an entry with the error result of each tool call
// of the last assistant message that has no result. A run that failed
// in its tool calls leaves them, and a provider rejects a call with no
// result. A resumed log has none, because [session.Log.Messages]
// repairs them. The state does not read the log again after a run, so
// turn 1 of the next run repairs them.
func (a *Agent) interrupted() []session.Entry {
	msgs := a.State().Messages()
	last := -1
	for i, m := range msgs {
		if m.Role == ai.RoleAssistant {
			last = i
		}
	}
	if last < 0 {
		return nil
	}

	answered := make(map[string]bool)
	for _, m := range msgs[last+1:] {
		if m.Role != ai.RoleToolResult {
			return nil
		}
		answered[m.ToolCallID] = true
	}
	var entries []session.Entry
	for _, tc := range msgs[last].ToolCalls() {
		if !answered[tc.ID] {
			result := session.Interrupted(tc.ID)
			entries = append(entries, session.NewEntry(result, session.Author(a.cfg.name)))
		}
	}
	return entries
}

// turn runs one turn. It emits turn_start. In turn 1, it adds the
// interrupted results. Then it adds the system change if the sections
// or the tools changed, then the input and next. Then it calls the
// model and runs the tool calls that the model makes. It returns the
// messages of the turn without the input, and reports if the model
// called a tool. It emits turn_end on every exit, so the pair stays
// balanced. turn_end carries what the turn produced before it ended.
func (a *Agent) turn(
	ctx context.Context,
	turn int,
	input []session.Entry,
	next []session.Entry,
	em *emitter,
) (out []ai.Message, calledTools bool, err error) {
	var (
		assistant *ai.Message
		results   []ai.Message
	)
	em.emit(EventTurnStart{Turn: turn})
	defer func() {
		em.emit(EventTurnEnd{
			Message:     assistant,
			ToolResults: results,
		})
	}()

	// A run that failed in its tool calls leaves calls with no result.
	// Their results go first, after their calls.
	if turn == 1 {
		msgs, err := a.add(ctx, em, a.interrupted()...)
		out = append(out, msgs...)
		if err != nil {
			return out, false, err
		}
	}

	// The system change goes before the new messages, so the model reads
	// the prompt before the messages that it applies to.
	change, err := a.systemChange(ctx, a.State().Messages())
	if err != nil {
		return out, false, err
	}
	if change != nil {
		msgs, err := a.add(ctx, em, session.NewEntry(*change))
		out = append(out, msgs...)
		if err != nil {
			return out, false, err
		}
	}
	if _, err := a.add(ctx, em, input...); err != nil {
		return out, false, err
	}
	msgs, err := a.add(ctx, em, next...)
	out = append(out, msgs...)
	if err != nil {
		return out, false, err
	}
	if err := ctx.Err(); err != nil {
		return out, false, err
	}

	msgs = a.State().Messages()
	assistant, err = a.call(ctx, msgs, em)
	if assistant != nil {
		out = append(out, *assistant)
	}
	if err != nil {
		return out, false, err
	}

	// A finished call runs whatever the stop reason, except a refusal: a
	// safety classifier stopped the message, so its calls must not run.
	// A call that does not run gets an error result from the provider,
	// and the model never reads the output of the tool.
	calls := assistant.ToolCalls()
	if len(calls) == 0 || assistant.StopReason == ai.StopReasonRefusal {
		return out, false, nil
	}
	// The tools that the model can call change only at the next model
	// call, so the calls of the turn share one fold of the messages.
	declared := transcript.CurrentTools(nil, msgs)
	batches := [][]ai.ToolCall{calls}
	if a.runsInOrder(declared, calls) {
		batches = nil
		for _, tc := range calls {
			batches = append(batches, []ai.ToolCall{tc})
		}
	}
	for _, batch := range batches {
		var res []ai.Message
		res, err = a.runTools(ctx, declared, batch, em)
		results = append(results, res...)
		if err != nil {
			break
		}
	}
	out = append(out, results...)
	return out, true, err
}
