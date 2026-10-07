package openaichat_test

import (
	"context"
	"fmt"
	"os"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/openaichat"
)

// A model on another server is a model of your own. It sets MaxTokens,
// because Fireworks caps a call at 2048 tokens by default.
func Example_fireworks() {
	ctx := context.Background()

	kimi := ai.Model{
		ID:        "accounts/fireworks/models/kimi-k2-thinking",
		Name:      "Kimi K2 Thinking",
		MaxTokens: 16384,
	}
	p := openaichat.New(
		openaichat.APIKey(os.Getenv("FIREWORKS_API_KEY")),
		openaichat.BaseURL("https://api.fireworks.ai/inference/v1"),
	)
	lm := ai.LanguageModel{Model: kimi, Provider: p}

	msg, err := lm.StreamText(ctx, []ai.Message{ai.UserMessage("Hi")}).Wait()
	if err != nil {
		panic(err)
	}
	fmt.Println(msg.Text())
}
