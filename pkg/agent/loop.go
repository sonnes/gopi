package agent

import (
	"context"

	"github.com/sonnes/gopi/internal/transcript"
	"github.com/sonnes/gopi/pkg/ai"
)

// run is the producer behind [Agent.Run]. It returns the messages that
// it produced, with the error if the run failed. The input is not in
// the result, because the caller already has it. The order of events:
//
//	agent_start
//	turn_start
//	  message_start, message_end...                     (system change, if any)
//	  message_start, message_end...                     (new messages)
//	  message_start, message_update..., message_end     (assistant)
//	  tool_execution_start...                           (each call, in order)
//	  tool_execution_end...                             (as each call ends)
//	  message_start, message_end...                     (each result, in order)
//
// With calls in order, each call emits its start, its end, and its
// result before the next call starts.
//
//	turn_end
//	message_start, message_end...                       (AfterTurn messages)
//	... the next turn, while the model calls tools or a message continues the run ...
//	agent_end
//
// The new messages of the first turn are the input, then the steer
// queue. The new messages of a later turn are the steer queue. If the
// turn before made no tool calls and the steer queue was empty, they
// are the follow-up queue. If that was empty too, they are the
// messages of the BeforeStop hooks.
//
// [Agent.Run] sends agent_start before run. agent_end comes on every
// exit, with the error of a run that failed.
func (a *Agent) run(
	ctx context.Context,
	input []ai.Message,
	emit func(Event),
) (produced []ai.Message, err error) {
	defer func() {
		end := EventAgentEnd{Usage: a.State().Usage()}
		if err != nil {
			end.Error = err.Error()
		}
		emit(end)
	}()

	next := a.drain(&a.state.steer)
	for turn := 1; ; turn++ {
		out, calledTools, err := a.turn(ctx, turn, input, next, emit)
		produced = append(produced, out...)
		if err != nil {
			return produced, err
		}
		input = nil

		added, err := a.afterTurn(ctx, emit)
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
// each hook, it adds the messages of the hook, so the next hook sees
// them in its state. It returns the messages.
func (a *Agent) afterTurn(ctx context.Context, emit func(Event)) ([]ai.Message, error) {
	var added []ai.Message
	for _, h := range a.cfg.hooks {
		msgs, err := h.AfterTurn(ctx, a.State())
		if err != nil {
			return added, err
		}
		a.add(emit, msgs...)
		added = append(added, msgs...)
	}
	return added, nil
}

// next returns the messages that the agent reads in the next turn. It
// drains the steer queue. If the queue was empty and the turn made no
// tool calls, the run would stop: next then drains the follow-up queue,
// and if that was empty too, runs the BeforeStop hooks. With no
// messages and no tool calls, the run stops.
func (a *Agent) next(ctx context.Context, calledTools bool) ([]ai.Message, error) {
	if steer := a.drain(&a.state.steer); len(steer) > 0 {
		return steer, nil
	}
	if calledTools {
		return nil, nil
	}
	if followUp := a.drain(&a.state.followUp); len(followUp) > 0 {
		return followUp, nil
	}
	return a.beforeStop(ctx)
}

// beforeStop runs the BeforeStop hooks until one continues the run. It
// returns the messages of that hook, or nil if the run stops.
func (a *Agent) beforeStop(ctx context.Context) ([]ai.Message, error) {
	for _, h := range a.cfg.hooks {
		msgs, err := h.BeforeStop(ctx, a.State())
		if err != nil {
			return nil, err
		}
		if len(msgs) > 0 {
			return msgs, nil
		}
	}
	return nil, nil
}

// add adds msgs to the history, one at a time. Each message gets
// message_start and message_end, so a consumer sees it as a message.
func (a *Agent) add(emit func(Event), msgs ...ai.Message) {
	for _, m := range msgs {
		emit(EventMessageStart{Message: m})
		emit(EventMessageEnd{Message: m})
	}
}

// turn runs one turn. It emits turn_start, then adds the system change
// if the sections or the tools changed, then the input and next. Then
// it calls the model and runs the tool calls that the model makes. It
// returns the messages of the turn without the input, and reports if
// the model called a tool. It emits turn_end on every exit, so the pair
// stays balanced. turn_end carries what the turn produced before it
// ended.
func (a *Agent) turn(
	ctx context.Context,
	turn int,
	input []ai.Message,
	next []ai.Message,
	emit func(Event),
) (out []ai.Message, calledTools bool, err error) {
	var (
		assistant *ai.Message
		results   []ai.Message
	)
	emit(EventTurnStart{Turn: turn})
	defer func() {
		emit(EventTurnEnd{
			Message:     assistant,
			ToolResults: results,
		})
	}()

	// The system change goes before the new messages, so the model reads
	// the prompt before the messages that it applies to.
	change, err := a.systemChange(ctx, a.State().Messages())
	if err != nil {
		return nil, false, err
	}
	if change != nil {
		a.add(emit, *change)
		out = append(out, *change)
	}
	a.add(emit, input...)
	a.add(emit, next...)
	out = append(out, next...)
	if err := ctx.Err(); err != nil {
		return out, false, err
	}

	msgs := a.State().Messages()
	assistant, err = a.call(ctx, msgs, emit)
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
	// call, so the calls of the turn share one fold of the history.
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
		res, err = a.runTools(ctx, declared, batch, emit)
		results = append(results, res...)
		if err != nil {
			break
		}
	}
	out = append(out, results...)
	return out, true, err
}
