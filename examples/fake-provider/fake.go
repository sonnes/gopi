// Package fake is a provider for tests. It answers each call with the
// next scripted message and records the messages of each call.
//
// A test binds it like any provider:
//
//	p := &fake.Provider{Replies: []ai.Message{ai.AssistantMessage(ai.Text{Text: "hi"})}}
//	lm := ai.LanguageModel{Model: ai.Model{ID: "fake"}, Provider: p}
package fake

import (
	"context"
	"errors"

	"github.com/sonnes/gopi/pkg/ai"
)

// Provider answers with scripted replies.
type Provider struct {
	// Replies are the answers, in order. Each call consumes one.
	Replies []ai.Message
	// Calls holds the messages of each call, in order.
	Calls [][]ai.Message
	// Options holds the options of each call, in order.
	Options []ai.Options
}

var _ ai.TextProvider = (*Provider)(nil)

// StreamText records the call and streams the next reply. If no reply
// is left, the stream fails at once.
func (p *Provider) StreamText(
	ctx context.Context,
	model ai.Model,
	messages []ai.Message,
	opts ai.Options,
) *ai.EventStream {
	p.Calls = append(p.Calls, messages)
	p.Options = append(p.Options, opts)

	if len(p.Replies) == 0 {
		return ai.ErrStream(errors.New("fake: no reply left"))
	}
	reply := p.Replies[0]
	p.Replies = p.Replies[1:]
	reply.Model = model.ID

	return ai.NewEventStream(func(push func(ai.Event)) (*ai.Message, error) {
		push(ai.Event{Type: ai.EventStart})
		for i, c := range reply.Content {
			switch b := c.(type) {
			case ai.Text:
				push(ai.Event{Type: ai.EventTextStart, ContentIndex: i})
				push(ai.Event{Type: ai.EventTextDelta, ContentIndex: i, Delta: b.Text})
				push(ai.Event{Type: ai.EventTextEnd, ContentIndex: i, Content: b.Text})
			case ai.ToolCall:
				call := b
				push(ai.Event{Type: ai.EventToolStart, ContentIndex: i})
				push(ai.Event{Type: ai.EventToolEnd, ContentIndex: i, ToolCall: &call})
			}
		}
		return &reply, nil
	})
}
