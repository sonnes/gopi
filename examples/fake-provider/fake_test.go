package fake_test

import (
	"context"
	"strings"
	"testing"

	"github.com/sonnes/gopi/examples/fake-provider"
	"github.com/sonnes/gopi/pkg/ai"
)

// Greet is the code under test. It takes a binding, so a test passes a
// fake and production passes a real provider.
func Greet(ctx context.Context, lm ai.LanguageModel, name string) (string, error) {
	msg, err := lm.StreamText(ctx, []ai.Message{
		ai.UserMessage("Greet " + name + " in one sentence."),
	}, ai.SystemPrompt([]ai.Content{ai.Text{Text: "You are terse."}})).Wait()
	if err != nil {
		return "", err
	}
	return msg.Text(), nil
}

func TestGreet(t *testing.T) {
	p := &fake.Provider{
		Replies: []ai.Message{
			ai.AssistantMessage(ai.Text{Text: "Hello, Ada."}),
		},
	}
	lm := ai.LanguageModel{Model: ai.Model{ID: "fake"}, Provider: p}

	got, err := Greet(context.Background(), lm, "Ada")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Hello, Ada." {
		t.Errorf("got %q", got)
	}

	// The fake recorded what the code sent.
	if len(p.Calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(p.Calls))
	}
	if !strings.Contains(p.Calls[0][0].Text(), "Ada") {
		t.Errorf("prompt = %q", p.Calls[0][0].Text())
	}
	system, ok := ai.GetOption(p.Options[0], ai.SystemPrompt)
	if !ok || len(system) != 1 || system[0] != (ai.Text{Text: "You are terse."}) {
		t.Errorf("system = %v, set = %v", system, ok)
	}
}

func TestGreet_ProviderError(t *testing.T) {
	// No replies, so the stream fails and Greet returns the error.
	lm := ai.LanguageModel{Model: ai.Model{ID: "fake"}, Provider: &fake.Provider{}}

	if _, err := Greet(context.Background(), lm, "Ada"); err == nil {
		t.Fatal("want an error")
	}
}
