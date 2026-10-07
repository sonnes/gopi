// Command hello makes one model call and prints the answer.
//
// Set ANTHROPIC_API_KEY before you run it.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/anthropic"
)

func main() {
	ctx := context.Background()

	model := ai.LanguageModel{
		Model:    anthropic.Sonnet5,
		Provider: anthropic.New(),
	}

	messages := []ai.Message{
		ai.UserMessage("Say hello in one sentence."),
	}

	msg, err := model.StreamText(ctx, messages).Wait()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(msg.Text())
}
