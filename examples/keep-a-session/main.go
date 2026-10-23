// Command keep-a-session keeps the log of an agent in a JSONL file.
// Run it twice. The first run tells the agent a name. The second run
// resumes the session and asks for the name.
//
// Set ANTHROPIC_API_KEY before you run it.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/anthropic"
	"github.com/sonnes/gopi/pkg/session"
	"github.com/sonnes/gopi/pkg/session/jsonl"
)

func main() {
	ctx := context.Background()

	model := ai.LanguageModel{
		Model:    ai.Model{ID: "claude-sonnet-5"},
		Provider: anthropic.New(),
	}

	dir := filepath.Join(os.TempDir(), "gopi-sessions")
	store, err := jsonl.New(dir)
	if err != nil {
		log.Fatal(err)
	}

	// Durable creates the session on the first run and resumes it after.
	a, err := agent.Durable(ctx, model, "demo",
		agent.Store(store),
		agent.Name("helper"),
		agent.System(func(context.Context) ([]ai.Section, error) {
			return []ai.Section{{Name: "base", Text: "Answer in one sentence."}}, nil
		}),
	)
	if err != nil {
		log.Fatal(err)
	}

	question := "My name is Ada. Remember it."
	if len(a.Log().Entries()) > 0 {
		question = "What is my name?"
	}

	// The clock is true only now, so it is ephemeral: the model reads it,
	// and the file never holds it.
	clock := "The time is " + time.Now().Format(time.Kitchen) + "."

	stream := a.Run(ctx,
		session.Text(question, session.Author("user-1")),
		session.Text(clock, session.Ephemeral()),
	)

	for ev, err := range stream.Events() {
		if err != nil {
			log.Fatal(err)
		}
		// The log stores each message before its message_end, so the
		// message of message_end is in the file. An ephemeral message is
		// not, and neither is a message with no content.
		end, ok := ev.(agent.EventMessageEnd)
		stored := ok && !end.Ephemeral && len(end.Message.Content) > 0
		if stored {
			fmt.Printf("stored %s\n", end.Message.Role)
		}
	}

	msgs, err := stream.Wait()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(msgs[len(msgs)-1].Text())
	fmt.Println(filepath.Join(dir, "demo.jsonl"))
}
