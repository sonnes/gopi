package openai_test

import (
	"context"
	"fmt"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/openai"
)

// The calls of one conversation share a cache key, so they hit the same
// cache.
func ExamplePromptCacheKey() {
	ctx := context.Background()
	lm := ai.LanguageModel{Model: openai.GPT6Luna, Provider: openai.New()}
	system := "You are a helpful assistant."
	conversationID := "conv-42"

	opts := ai.JoinOptions(
		ai.SystemPrompt([]ai.Content{ai.Text{Text: system}}),
		openai.PromptCacheKey(conversationID),
	)
	msg, err := lm.StreamText(ctx, []ai.Message{ai.UserMessage("Hi")}, opts).Wait()
	if err != nil {
		panic(err)
	}
	fmt.Println(msg.Usage.CacheRead)
}

func ExampleProvider_GenerateImage() {
	ctx := context.Background()

	im := ai.ImageModel{Model: openai.GPTImage2, Provider: openai.New()}
	resp, err := im.GenerateImage(ctx, "A blue circle", ai.ImageSize("1024x1024"))
	if err != nil {
		panic(err)
	}
	fmt.Println(resp.Images[0].MediaType)
}
