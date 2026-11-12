package gemini

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"google.golang.org/genai/interactions/models/interactions"

	"github.com/sonnes/gopi/pkg/ai"
)

const emptyArguments = "{}"

type block struct {
	index     int
	kind      ai.EventType
	text      strings.Builder
	signature string
	// args holds the arguments of the start event, and deltas holds the
	// arguments of the delta events.
	id     string
	name   string
	args   map[string]any
	deltas strings.Builder
	ended  bool
}

// Each step that becomes content gets the next index, so the blocks get
// the same index as in the final message.
type streamState struct {
	push    func(ai.Event)
	started bool
	blocks  []*block
	byStep  map[int]*block
	status  interactions.InteractionSseEventInteractionStatus
	usage   *interactions.Usage
}

func newStreamState(push func(ai.Event)) *streamState {
	return &streamState{
		push:   push,
		byStep: map[int]*block{},
	}
}

// handle returns true on the event that completes the interaction. The
// docs of the API tell a client to skip an event type that it does not
// know.
func (s *streamState) handle(ev *interactions.InteractionSSEStreamEvent) (bool, error) {
	if !s.started {
		s.started = true
		s.push(ai.Event{Type: ai.EventStart})
	}

	data := ev.GetData()
	switch {
	case data.StepStart != nil:
		s.start(data.StepStart)
	case data.StepDelta != nil:
		s.delta(data.StepDelta)
	case data.StepStop != nil:
		if b, ok := s.byStep[data.StepStop.Index]; ok {
			s.end(b)
		}
	case data.InteractionCompletedEvent != nil:
		s.status = data.InteractionCompletedEvent.Interaction.Status
		s.usage = data.InteractionCompletedEvent.Interaction.Usage
		return true, nil
	case data.ErrorEvent != nil:
		return false, eventError(data.ErrorEvent)
	}
	return false, nil
}

// A step that is not text, a thought, or a function call, such as the
// step of a server tool, gets no block.
func (s *streamState) start(ev *interactions.StepStart) {
	step := ev.Step
	var b *block
	switch {
	case step.ModelOutputStep != nil:
		b = s.open(ev.Index, ai.EventTextStart)
		for _, text := range textParts(step.ModelOutputStep.Content) {
			s.appendText(b, text)
		}
	case step.ThoughtStep != nil:
		b = s.open(ev.Index, ai.EventThinkStart)
		if sig := step.ThoughtStep.Signature; sig != nil {
			b.signature = *sig
		}
		for _, c := range step.ThoughtStep.Summary {
			if c.TextContent != nil {
				s.appendText(b, c.TextContent.Text)
			}
		}
	case step.FunctionCallStep != nil:
		b = s.open(ev.Index, ai.EventToolStart)
		b.id = step.FunctionCallStep.ID
		b.name = step.FunctionCallStep.Name
		b.args = step.FunctionCallStep.Arguments
	}
}

func (s *streamState) open(step int, kind ai.EventType) *block {
	b := &block{
		index: len(s.blocks),
		kind:  kind,
	}
	s.blocks = append(s.blocks, b)
	s.byStep[step] = b
	s.push(ai.Event{Type: kind, ContentIndex: b.index})
	return b
}

func (s *streamState) appendText(b *block, text string) {
	if text == "" {
		return
	}
	b.text.WriteString(text)
	kind := ai.EventTextDelta
	if b.kind == ai.EventThinkStart {
		kind = ai.EventThinkDelta
	}
	s.push(ai.Event{Type: kind, ContentIndex: b.index, Delta: text})
}

func (s *streamState) delta(ev *interactions.StepDelta) {
	b, ok := s.byStep[ev.Index]
	if !ok {
		return
	}

	d := ev.Delta
	switch {
	case d.TextDelta != nil:
		s.appendText(b, d.TextDelta.Text)
	case d.ThoughtSummaryDelta != nil:
		if c := d.ThoughtSummaryDelta.Content; c != nil && c.TextContent != nil {
			s.appendText(b, c.TextContent.Text)
		}
	case d.ThoughtSignatureDelta != nil:
		if sig := d.ThoughtSignatureDelta.Signature; sig != nil {
			b.signature = *sig
		}
	case d.ArgumentsDelta != nil:
		if args := d.ArgumentsDelta.Arguments; args != nil && *args != "" {
			b.deltas.WriteString(*args)
			s.push(ai.Event{Type: ai.EventToolDelta, ContentIndex: b.index, Delta: *args})
		}
	}
}

// end pushes the end event of a block once.
func (s *streamState) end(b *block) {
	if b.ended {
		return
	}
	b.ended = true

	ev := ai.Event{ContentIndex: b.index}
	switch b.kind {
	case ai.EventTextStart:
		ev.Type = ai.EventTextEnd
		ev.Content = b.text.String()
	case ai.EventThinkStart:
		ev.Type = ai.EventThinkEnd
	case ai.EventToolStart:
		call := b.toolCall()
		if b.deltas.Len() == 0 {
			// The start event held the whole input, so the stream has
			// no delta of it yet.
			s.push(ai.Event{Type: ai.EventToolDelta, ContentIndex: b.index, Delta: string(call.Input)})
		}
		ev.Type = ai.EventToolEnd
		ev.ToolCall = &call
	}
	s.push(ev)
}

// The arguments of the delta events win over the arguments of the start
// event. A call with no arguments gets the empty object.
func (b *block) toolCall() ai.ToolCall {
	input := b.deltas.String()
	if input == "" && len(b.args) > 0 {
		// The SDK decoded the arguments from JSON, so they encode.
		data, _ := json.Marshal(b.args)
		input = string(data)
	}
	if input == "" {
		input = emptyArguments
	}
	return ai.ToolCall{
		ID:    b.id,
		Name:  b.name,
		Input: json.RawMessage(input),
	}
}

func (b *block) content() ai.Content {
	switch b.kind {
	case ai.EventThinkStart:
		return ai.ThinkingBlock{
			Thinking:  b.text.String(),
			Signature: signature{Gemini: b.signature}.String(),
		}
	case ai.EventToolStart:
		return b.toolCall()
	default:
		return ai.Text{Text: b.text.String()}
	}
}

// message ends each block that is still open. An interaction that
// failed or that a caller cancelled is an error, and it ends no block.
func (s *streamState) message(model ai.Model) (*ai.Message, error) {
	hasToolCall := slices.ContainsFunc(s.blocks, func(b *block) bool {
		return b.kind == ai.EventToolStart
	})
	status := interactions.InteractionStatus(s.status)
	reason, err := stopReason(status, hasToolCall)
	if err != nil {
		return nil, err
	}

	var content []ai.Content
	for _, b := range s.blocks {
		s.end(b)
		content = append(content, b.content())
	}
	msg := &ai.Message{
		Role:       ai.RoleAssistant,
		Model:      model.ID,
		Content:    content,
		StopReason: reason,
		Usage:      usageOf(s.usage, model),
	}
	return msg, nil
}

func eventError(ev *interactions.ErrorEvent) error {
	if ev.Error == nil {
		return errors.New("gemini: stream error")
	}
	code := stringValue(ev.Error.Code)
	message := stringValue(ev.Error.Message)
	return fmt.Errorf("gemini: %s: %s", code, message)
}

func stringValue(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
