// Command make-a-decision asks three closed questions about a support
// ticket on a decision model, and prints each answer with its
// confidence.
//
// Set OPENROUTER_API_KEY before you run it.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/openrouter"
)

func main() {
	ctx := context.Background()

	dm := ai.DecisionModel{
		Model:    ai.Model{ID: "typesafe/jev-1.14"},
		Provider: openrouter.New(),
	}
	ticket := []ai.Message{
		ai.UserMessage("Checkout shows a blank page after I click Pay. No customer can pay."),
	}

	urgency := ai.Score("urgency", "How urgent is this ticket?",
		ai.ScoreLevel{Label: "later"},
		ai.ScoreLevel{Label: "this week"},
		ai.ScoreLevel{Label: "now", Description: "It blocks revenue."},
	)

	resp, err := dm.Decide(ctx, ticket,
		ai.YesNo("is_bug", "Does the user report a software defect?").
			YesWhen("The user describes broken behavior.").
			NoWhen("The user asks a question."),
		ai.Choice("team", "Which team owns this ticket?",
			ai.ChoiceOption{ID: "payments", Description: "Checkout, billing, or payments."},
			ai.ChoiceOption{ID: "account", Description: "Login, password, or profile."},
		),
		urgency,
		ai.MaxRetries(2),
	)
	if err != nil {
		log.Fatal(err)
	}

	route(resp)

	for _, a := range resp.Answers {
		fmt.Printf("%-8s confidence %.2f\n", a.Name, a.Confidence)
	}
	fmt.Printf("input %d tokens, $%.6f\n", resp.Usage.Input, resp.Usage.Cost.Total())
}

// route reads each answer by its name. An accessor returns an error if
// the model refused the question.
func route(resp *ai.DecisionResponse) {
	isBug, err := resp.YesNo("is_bug")
	if errors.Is(err, ai.ErrRefused) {
		fmt.Println("is_bug: the model refused, so a person triages the ticket")
		return
	}
	if err != nil {
		log.Fatal(err)
	}
	team, err := resp.Choice("team")
	if err != nil {
		log.Fatal(err)
	}
	urgency, err := resp.Score("urgency")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("bug %.2f, team %s, urgency %.2f of 2\n", isBug, team, urgency)
}
