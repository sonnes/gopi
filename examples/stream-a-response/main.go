// Command stream-a-response shows each delta as it arrives. Thinking
// goes to stderr and text goes to stdout, so a reader can tell them
// apart. The second turn sends the signed thinking block back.
//
// Set ANTHROPIC_API_KEY before you run it.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/anthropic"
)

func main() {
	ctx := context.Background()

	model := ai.LanguageModel{
		Model:    ai.Model{ID: "claude-sonnet-5"},
		Provider: anthropic.New(),
	}

	messages := []ai.Message{
		ai.UserMessage("What is 17 * 24? Show your working, then the result."),
	}

	// The thinking level travels as an option. The provider maps it to
	// its own parameter.
	stream := model.StreamText(ctx, messages, ai.Thinking(ai.ThinkingMedium))

	for ev, err := range stream.Events() {
		if err != nil {
			log.Fatal(err)
		}
		switch ev.Type {
		case ai.EventThinkDelta:
			fmt.Fprint(os.Stderr, ev.Delta)
		case ai.EventThinkEnd:
			fmt.Fprintln(os.Stderr)
		case ai.EventTextDelta:
			fmt.Print(ev.Delta)
		case ai.EventTextEnd:
			fmt.Println()
		}
	}

	// Wait returns the final message after the last event.
	msg, err := stream.Wait()
	if err != nil {
		log.Fatal(err)
	}

	for _, c := range msg.Content {
		if b, ok := c.(ai.ThinkingBlock); ok {
			fmt.Printf("thinking (%d chars, signed=%v)\n", len(b.Thinking), b.Signature != "")
		}
	}
	fmt.Println("blocks:", len(msg.Content), "stop:", msg.StopReason)

	// Send the assistant message back unchanged. The signed thinking
	// block goes with it, and the provider verifies the signature.
	messages = append(messages, *msg, ai.UserMessage("And half of that?"))

	msg, err = model.StreamText(ctx, messages, ai.Thinking(ai.ThinkingMedium)).Wait()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("text:", msg.Text())
}

// forward writes one event as one line of JSON. A web handler calls it
// for each event to send the stream to a browser. The encoder omits
// the zero fields, so a text delta is a small object.
func forward(w *json.Encoder, ev ai.Event) error {
	return w.Encode(ev)
}
