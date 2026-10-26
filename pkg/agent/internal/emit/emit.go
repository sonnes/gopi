// Package emit turns the output of an agent CLI into [agent.Event]
// values and log entries. The CLI runs its own loop and its own tools.
// An [Emitter] mirrors that loop, with the same event order as
// [agent.Agent]:
//
//	agent_start
//	  turn_start
//	    message_start, message_end...                    (input, turn 1 only)
//	    message_start, message_update..., message_end    (assistant)
//	    tool_execution_start, tool_execution_end,
//	    message_start, message_end                       (per tool call)
//	  turn_end
//	  ...
//	agent_end
//
// A turn is one assistant message and the tool results that follow it.
// The next assistant message starts the next turn.
//
// The log stores each message before its message_end, so a consumer
// that reads a message_end knows that the store has the message. A
// message_end with no content is the end of a message that the log
// does not keep.
//
// Each event changes a [State] before the stream gets it, so the state
// of a CLI agent follows its runs as the state of [agent.Agent] does.
//
// An Emitter belongs to one run. The caller calls its methods from one
// goroutine.
package emit

import (
	"context"
	"errors"
	"sync"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/internal/partial"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
)

// State is the [agent.State] of a CLI agent. It lives as long as the
// agent, and each event of a run changes it. It is safe for concurrent
// use.
type State struct {
	mu    sync.Mutex
	state agent.State
	log   session.Writer
}

// NewState returns the state of an agent over log. Its messages are
// the messages of log, such as the messages of a resumed session.
func NewState(log *session.Log) *State {
	s := &State{log: log.Writer()}
	for _, m := range log.Messages() {
		s.state.Apply(agent.EventMessageEnd{Message: m})
	}
	return s
}

// Get returns a copy of the state, with the log of the agent. The copy
// has no queues, so [agent.State.Steer] does nothing.
func (s *State) Get() agent.State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Clip().WithLog(s.log)
}

// apply changes the state by e.
func (s *State) apply(e agent.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Apply(e)
}

// start applies agent_start, or fails if a run is active. The check
// and the change are one step under the lock, so two runs never start
// together.
func (s *State) start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Running() {
		return errors.New("a run is active")
	}
	s.state.Apply(agent.EventAgentStart{})
	return nil
}

// Emitter sends the events of one run and writes its messages to the
// log. The run owns it.
type Emitter struct {
	log   *session.Log
	name  string
	state *State
	push  func(agent.Event)

	produced []ai.Message

	// turnNum is the number of the last turn that started.
	turnNum int
	inTurn  bool
	// turn holds the assistant message and the tool results of the open
	// turn.
	turn []ai.Message
	// msg is the open assistant message, or nil.
	msg *ai.Message
	// tools holds the open tool calls, by call ID, in start order.
	tools []toolCall
	// usage is the sum of the usage of each assistant message that the
	// run ended, stored or not.
	usage ai.Usage
}

// toolCall is a tool call that has a start event and no end event.
type toolCall struct {
	id   string
	name string
}

// New returns an Emitter that writes to log, changes state, and sends
// events to push. Name is the Author of every entry that it writes.
func New(log *session.Log, name string, state *State, push func(agent.Event)) *Emitter {
	return &Emitter{
		log:   log,
		name:  name,
		state: state,
		push:  push,
	}
}

// Start sends agent_start. It fails with no event if a run of the same
// state is active. After Start, the run must end with
// [Emitter.Finish].
func (e *Emitter) Start() error {
	if err := e.state.start(); err != nil {
		return err
	}
	e.push(agent.EventAgentStart{})
	return nil
}

// Input opens turn 1 and stores the input of the run in it. Each
// message gets message_start and message_end after the store. An
// ephemeral message gets its events, and the log does not keep it. A
// custom entry is stored with no event.
func (e *Emitter) Input(ctx context.Context, input []session.Entry) error {
	e.openTurn()
	for _, en := range input {
		if en.Kind != "" {
			if err := e.commit(ctx, en); err != nil {
				return err
			}
			continue
		}
		err := e.commit(ctx, en,
			agent.EventMessageStart{Message: en.Message},
			agent.EventMessageEnd{
				Message:   en.Message,
				Ephemeral: en.Ephemeral,
			},
		)
		if err != nil {
			return err
		}
	}
	return nil
}

// Update applies one model event to the open assistant message and
// sends message_update. If no assistant message is open, Update opens
// one first. See [Emitter.Begin].
func (e *Emitter) Update(evt ai.Event) {
	e.Begin()
	partial.Accumulate(e.msg, &evt)
	e.emit(agent.EventMessageUpdate{
		Message:        *partial.Snapshot(e.msg),
		AssistantEvent: evt,
	})
}

// Partial returns a copy of the open assistant message, or nil if no
// message is open.
func (e *Emitter) Partial() *ai.Message {
	if e.msg == nil {
		return nil
	}
	return partial.Snapshot(e.msg)
}

// Begin opens an assistant message and sends message_start. If a
// message is open, Begin does nothing. If the open turn already has an
// assistant message, Begin ends that turn and starts the next one.
func (e *Emitter) Begin() {
	if e.msg != nil {
		return
	}
	turnHasAssistant := len(e.turn) > 0
	if turnHasAssistant {
		e.endTurn()
	}
	e.openTurn()
	e.msg = &ai.Message{Role: ai.RoleAssistant}
	e.emit(agent.EventMessageStart{Message: *partial.Snapshot(e.msg)})
}

// End stores the final assistant message and sends message_end. If no
// message is open, End opens one first. If the store fails, the
// message_end has no content, and End returns the error.
func (e *Emitter) End(ctx context.Context, final ai.Message) error {
	e.Begin()
	final.Role = ai.RoleAssistant
	e.msg = nil
	e.usage = e.usage.Add(final.Usage)

	entry := session.NewEntry(final, session.Author(e.name))
	if err := e.commit(ctx, entry, agent.EventMessageEnd{Message: final}); err != nil {
		e.emit(emptyEnd(final.Usage))
		return err
	}
	e.produced = append(e.produced, final)
	e.turn = append(e.turn, final)
	return nil
}

// Drop closes the open assistant message with a message_end that has
// no content, and does not store it. It is for a message that the log
// does not keep, such as a message with no content. If no message is
// open, Drop does nothing.
func (e *Emitter) Drop() {
	if e.msg == nil {
		return
	}
	e.usage = e.usage.Add(e.msg.Usage)
	e.emit(emptyEnd(e.msg.Usage))
	e.msg = nil
}

// Usage returns the sum of the usage of each assistant message that
// the run ended, stored or not. It is the usage of a run that fails
// before the CLI reports its own sum.
func (e *Emitter) Usage() ai.Usage { return e.usage }

// Subagent sends one subagent event: an [agent.EventSubagentStart],
// an [agent.EventSubagent], or an [agent.EventSubagentEnd]. The state
// ignores it, and the log stores nothing, because the child has its own
// session.
func (e *Emitter) Subagent(ev agent.Event) {
	e.emit(ev)
}

// ToolStart sends tool_execution_start for a call. The call must be in
// the last assistant message, which [Emitter.End] stored.
func (e *Emitter) ToolStart(tc ai.ToolCall) {
	e.tools = append(e.tools, toolCall{
		id:   tc.ID,
		name: tc.Name,
	})
	e.emit(agent.EventToolExecutionStart{
		ToolCallID: tc.ID,
		ToolName:   tc.Name,
		Args:       tc.Input,
	})
}

// ToolEnd sends tool_execution_end for the call with id, then stores
// the tool result message. If the call has no start event, ToolEnd
// does nothing and reports false, because a result without a call
// would break the log.
func (e *Emitter) ToolEnd(ctx context.Context, id string, result ai.ToolResult) (bool, error) {
	name, ok := e.closeTool(id)
	if !ok {
		return false, nil
	}
	e.emit(agent.EventToolExecutionEnd{
		ToolCallID: id,
		ToolName:   name,
		Result:     result,
	})

	msg := ai.ToolResultMessage(id, result.Content...)
	msg.IsError = result.IsError
	return true, e.result(ctx, msg)
}

// Finish ends the run. Every run that starts calls it once, on every
// exit. It closes what is open: an open assistant message gets a
// message_end with no content, and the log does not keep it. Each open
// tool call gets the result of [session.Interrupted], which the log
// stores, so the log has no call without a result. The open turn gets
// its turn_end. Then Finish sends agent_end with usage and the error
// of err.
//
// It returns the messages that the run stored, without the input, and
// err joined with any error of the store.
func (e *Emitter) Finish(ctx context.Context, usage ai.Usage, err error) ([]ai.Message, error) {
	e.Drop()
	for len(e.tools) > 0 {
		tc := e.tools[0]
		e.tools = e.tools[1:]
		msg := session.Interrupted(tc.id)
		e.emit(agent.EventToolExecutionEnd{
			ToolCallID: tc.id,
			ToolName:   tc.name,
			Result: ai.ToolResult{
				Content: msg.Content,
				IsError: true,
			},
		})
		if serr := e.result(ctx, msg); serr != nil {
			err = errors.Join(err, serr)
		}
	}
	if e.inTurn {
		e.endTurn()
	}

	end := agent.EventAgentEnd{Usage: usage}
	if err != nil {
		end.Error = err.Error()
	}
	e.emit(end)
	return e.produced, err
}

// result stores the tool result msg with its message events, and adds
// it to the open turn.
func (e *Emitter) result(ctx context.Context, msg ai.Message) error {
	entry := session.NewEntry(msg, session.Author(e.name))
	err := e.commit(ctx, entry,
		agent.EventMessageStart{Message: msg},
		agent.EventMessageEnd{Message: msg},
	)
	if err != nil {
		return err
	}
	e.produced = append(e.produced, msg)
	e.turn = append(e.turn, msg)
	return nil
}

// openTurn sends turn_start with the next turn number, if no turn is
// open.
func (e *Emitter) openTurn() {
	if e.inTurn {
		return
	}
	e.inTurn = true
	e.turnNum++
	e.emit(agent.EventTurnStart{Turn: e.turnNum})
}

// endTurn sends turn_end with the messages of the open turn.
func (e *Emitter) endTurn() {
	end := agent.EventTurnEnd{}
	if len(e.turn) > 0 {
		end.Message = &e.turn[0]
		end.ToolResults = e.turn[1:]
	}
	e.emit(end)
	e.inTurn = false
	e.turn = nil
}

// closeTool removes the open tool call with id and returns its name.
func (e *Emitter) closeTool(id string) (string, bool) {
	for i, tc := range e.tools {
		if tc.id == id {
			e.tools = append(e.tools[:i], e.tools[i+1:]...)
			return tc.name, true
		}
	}
	return "", false
}

// commit appends en to the log, then sends events. If the append
// fails, no event goes out. The append ignores the cancel of ctx, so a
// run that stops keeps the messages that it finished.
func (e *Emitter) commit(ctx context.Context, en session.Entry, events ...agent.Event) error {
	_, err := e.log.Append(context.WithoutCancel(ctx), en)
	if err != nil {
		return err
	}
	for _, ev := range events {
		e.emit(ev)
	}
	return nil
}

// emit changes the state by ev, then sends it.
func (e *Emitter) emit(ev agent.Event) {
	e.state.apply(ev)
	e.push(ev)
}

// emptyEnd returns the message_end of an assistant message that the
// log does not keep. A message with no content does not reach the
// state, but its usage counts.
func emptyEnd(usage ai.Usage) agent.EventMessageEnd {
	return agent.EventMessageEnd{
		Message: ai.Message{
			Role:  ai.RoleAssistant,
			Usage: usage,
		},
	}
}

// textSeparator joins adjacent text blocks of the input.
const textSeparator = "\n\n"

// Input returns the text and file blocks of the input entries, in
// order, for a CLI that takes one user message per turn. Adjacent text
// blocks join with a blank line. A custom entry and the other block
// types are skipped.
func Input(entries []session.Entry) []ai.Content {
	var out []ai.Content
	for _, en := range entries {
		if en.Kind != "" {
			continue
		}
		for _, c := range en.Message.Content {
			switch c := c.(type) {
			case ai.Text:
				out = appendText(out, c)
			case ai.File:
				out = append(out, c)
			}
		}
	}
	return out
}

// appendText appends t to out, or joins it to the last block if that
// block is text.
func appendText(out []ai.Content, t ai.Text) []ai.Content {
	if len(out) == 0 {
		return append(out, t)
	}
	last, ok := out[len(out)-1].(ai.Text)
	if !ok {
		return append(out, t)
	}
	out[len(out)-1] = ai.Text{Text: last.Text + textSeparator + t.Text}
	return out
}

// Unread returns the text that stands in for a file of a media type
// that a CLI does not read, so that the model knows that the file was
// there.
func Unread(f ai.File) ai.Text {
	return ai.Text{Text: "[A file of type " + f.MediaType + " is not sent, because the CLI does not read it.]"}
}
