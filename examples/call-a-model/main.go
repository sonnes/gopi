// Command call-a-model makes one call and prints what comes back, then
// continues an answer that the output cap cut, then asks for a typed
// value instead of prose.
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

// Recipe is the shape of a typed answer. GenerateObject derives the
// JSON schema from the struct and decodes the response into it.
type Recipe struct {
	Name        string   `json:"name"`
	Ingredients []string `json:"ingredients"`
	Minutes     int      `json:"minutes" jsonschema:"total time in minutes"`
}

func main() {
	ctx := context.Background()

	model := ai.LanguageModel{
		Model:    anthropic.Sonnet5,
		Provider: anthropic.New(),
	}

	answer(ctx, model)
	continueAnswer(ctx, model)
	object(ctx, model)
}

// answer makes one call with a system prompt and an output cap.
func answer(ctx context.Context, model ai.LanguageModel) {
	messages := []ai.Message{
		ai.UserMessage("Name three Go proverbs."),
	}

	msg, err := model.StreamText(ctx, messages,
		ai.SystemPrompt([]ai.Content{ai.Text{Text: "You answer in plain lists. No introduction."}}),
		ai.MaxTokens(300),
	).Wait()
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(msg.Text())
	fmt.Println("stop:", msg.StopReason)
	fmt.Printf("usage: in=%d out=%d cache_read=%d cache_write=%d\n",
		msg.Usage.Input, msg.Usage.Output,
		msg.Usage.CacheRead, msg.Usage.CacheWrite)
}

// continueAnswer counts the input, caps the output hard, and asks the
// model to continue when the cap cuts the answer.
func continueAnswer(ctx context.Context, model ai.LanguageModel) {
	system := ai.SystemPrompt([]ai.Content{ai.Text{Text: "You write long answers."}})
	messages := []ai.Message{
		ai.UserMessage("Describe the Go memory model."),
	}

	// Count before the call. The count sends the same system prompt and
	// options, so it matches what the call will send.
	count, err := ai.CountTokens(ctx, model, messages, system)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("input tokens:", count.Total)

	var total ai.Usage

	msg, err := model.StreamText(ctx, messages, system, ai.MaxTokens(64)).Wait()
	if err != nil {
		log.Fatal(err)
	}
	total = total.Add(msg.Usage)
	fmt.Println("stop:", msg.StopReason)

	if msg.StopReason == ai.StopReasonLength {
		// Continue in a new turn. The messages carry the partial answer.
		messages = append(messages, *msg, ai.UserMessage("Continue."))
		msg, err = model.StreamText(ctx, messages, system, ai.MaxTokens(200)).Wait()
		if err != nil {
			log.Fatal(err)
		}
		total = total.Add(msg.Usage)
		fmt.Println("stop:", msg.StopReason)
	}

	fmt.Printf("usage: in=%d out=%d cache_read=%d cache_write=%d\n",
		total.Input, total.Output, total.CacheRead, total.CacheWrite)
}

// object asks for a Recipe. The provider must implement
// ai.ObjectProvider, or the call fails before any request.
func object(ctx context.Context, model ai.LanguageModel) {
	messages := []ai.Message{
		ai.UserMessage("A quick recipe for garlic bread."),
	}

	res, err := ai.GenerateObject[Recipe](ctx, model, messages)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("%s, %d minutes\n", res.Object.Name, res.Object.Minutes)
	for _, ing := range res.Object.Ingredients {
		fmt.Println("-", ing)
	}
	fmt.Println("raw:", res.Raw)
}
