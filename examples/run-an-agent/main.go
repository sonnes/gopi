// Command run-an-agent gives an agent one tool and prints the events of
// one run: the text, each tool call, and the usage. Then it prints the
// role of each message in the history of the agent.
//
// The model is the fake provider with scripted replies, so the program
// needs no key.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	fake "github.com/sonnes/gopi/examples/fake-provider"
	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/tool"
)

// WeatherIn is the input of the weather tool.
type WeatherIn struct {
	City string `json:"city" jsonschema:"the city to look up"`
}

// WeatherOut is the output. tool.New encodes it as JSON text.
type WeatherOut struct {
	City  string `json:"city"`
	TempC int    `json:"temp_c"`
}

var temps = map[string]int{"Lisbon": 21, "Oslo": 9}

var weather = tool.New(
	"weather",
	func(ctx context.Context, in WeatherIn) (WeatherOut, error) {
		return WeatherOut{City: in.City, TempC: temps[in.City]}, nil
	},
	tool.Description("Get the current weather of a city."),
)

// reply is one scripted answer of the fake model, with its usage.
func reply(in, out int, content ...ai.Content) ai.Message {
	m := ai.AssistantMessage(content...)
	m.Usage = ai.Usage{Input: in, Output: out}
	return m
}

// call is one scripted tool call of the fake model.
func call(id, city string) ai.ToolCall {
	return ai.ToolCall{
		ID:    id,
		Name:  "weather",
		Input: json.RawMessage(`{"city":"` + city + `"}`),
	}
}

// maxTurns is the most turns that one run can use.
const maxTurns = 5

func main() {
	ctx := context.Background()

	// The fake model calls the weather tool for two cities, then answers.
	lm := ai.LanguageModel{
		Model: ai.Model{ID: "fake"},
		Provider: &fake.Provider{Replies: []ai.Message{
			reply(120, 30, call("call_1", "Lisbon"), call("call_2", "Oslo")),
			reply(180, 12, ai.Text{Text: "Lisbon is warmer: 21 degrees against 9."}),
		}},
	}

	// The agent has no cap on the turns of a run. This hook is the cap.
	capTurns := agent.BeforeCall(func(ctx context.Context, s agent.State, r agent.Request) (agent.Request, error) {
		if s.Turn() > maxTurns {
			return r, errors.New("the run used too many turns")
		}
		return r, nil
	})

	a := agent.New(lm,
		agent.Toolset(weather),
		agent.System(func(context.Context) ([]ai.Section, error) {
			return []ai.Section{{Name: "base", Text: "Answer in one sentence."}}, nil
		}),
		capTurns,
	)

	var saved []ai.Message
	stream := a.Run(ctx, ai.UserMessage("Is it warmer in Lisbon or in Oslo?"))
	for ev, err := range stream.Events() {
		if err != nil {
			log.Fatal(err)
		}
		switch ev := ev.(type) {
		case agent.EventMessageUpdate:
			if ev.AssistantEvent.Type == ai.EventTextDelta {
				fmt.Print(ev.AssistantEvent.Delta)
			}
		case agent.EventToolExecutionStart:
			fmt.Printf("-> %s %s\n", ev.ToolName, ev.Args)
		case agent.EventToolExecutionEnd:
			fmt.Printf("<- %s error=%v\n", ev.ToolName, ev.Result.IsError)
		case agent.EventMessageEnd:
			saved = append(saved, ev.Message)
		case agent.EventAgentEnd:
			fmt.Printf("\n%d tokens in, %d tokens out\n", ev.Usage.Input, ev.Usage.Output)
		}
	}

	// The history holds the system message, the input, and each
	// message of the run.
	for _, m := range a.State().Messages() {
		fmt.Println(m.Role)
	}

	// saved holds the same messages. A new agent resumes from them.
	resumed := agent.New(lm, agent.History(saved...), agent.Toolset(weather))
	fmt.Printf("%d messages to resume\n", len(resumed.State().Messages()))
}
