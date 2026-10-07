// Command write-a-provider implements a provider that echoes the last
// user message, word by word. It shows the parts that every provider
// has: an option of its own, a pre-flight error, an event stream, and a
// final message. A second provider wraps the first and logs each call.
// It does not call a model, so it needs no key.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/sonnes/gopi/pkg/ai"
)

// Upper is an option that only this provider reads. It travels in the
// same list as ai.SystemPrompt and ai.MaxTokens.
var Upper = ai.NewOption[bool]()

// Echo is the provider. A real provider holds a client here.
type Echo struct{}

var _ ai.TextProvider = Echo{}

// StreamText streams the last user message back as text deltas.
func (Echo) StreamText(
	ctx context.Context,
	model ai.Model,
	messages []ai.Message,
	opts ai.Options,
) *ai.EventStream {
	// A pre-flight check fails on the stream, so the caller has one
	// error path.
	if len(messages) == 0 {
		return ai.ErrStream(errors.New("echo: no messages"))
	}
	text := messages[len(messages)-1].Text()

	// Unset is not false. A provider can tell the two apart.
	if upper, ok := ai.GetOption(opts, Upper); ok && upper {
		text = strings.ToUpper(text)
	}
	cap, _ := ai.GetOption(opts, ai.MaxTokens)

	return ai.NewEventStream(func(push func(ai.Event)) (*ai.Message, error) {
		push(ai.Event{Type: ai.EventStart})
		push(ai.Event{Type: ai.EventTextStart, ContentIndex: 0})

		words := strings.Fields(text)
		stop := ai.StopReasonStop
		if cap > 0 && len(words) > cap {
			words = words[:cap]
			stop = ai.StopReasonLength
		}

		var out strings.Builder
		for i, w := range words {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if i > 0 {
				w = " " + w
			}
			out.WriteString(w)
			push(ai.Event{Type: ai.EventTextDelta, ContentIndex: 0, Delta: w})
		}
		push(ai.Event{Type: ai.EventTextEnd, ContentIndex: 0, Content: out.String()})

		msg := ai.AssistantMessage(ai.Text{Text: out.String()})
		msg.Model = model.ID
		msg.StopReason = stop
		msg.Usage = ai.Usage{Input: len(text), Output: len(words)}
		return &msg, nil
	})
}

// logging is a provider that wraps another provider. It logs each call
// and returns the inner stream unchanged.
type logging struct {
	inner ai.TextProvider
}

func (l logging) StreamText(
	ctx context.Context,
	model ai.Model,
	messages []ai.Message,
	opts ai.Options,
) *ai.EventStream {
	log.Println("call", model.ID, len(messages), "messages")
	return l.inner.StreamText(ctx, model, messages, opts)
}

func main() {
	ctx := context.Background()

	model := ai.LanguageModel{
		Model: ai.Model{ID: "echo-1", Name: "Echo"},
		Provider: logging{
			inner: Echo{},
		},
	}

	messages := []ai.Message{ai.UserMessage("the quick brown fox")}

	stream := model.StreamText(ctx, messages, Upper(true), ai.MaxTokens(3))
	for ev, err := range stream.Events() {
		if err != nil {
			log.Fatal(err)
		}
		if ev.Type == ai.EventTextDelta {
			fmt.Printf("%q ", ev.Delta)
		}
	}
	msg, err := stream.Wait()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\n%s stop=%s\n", msg.Text(), msg.StopReason)
}
