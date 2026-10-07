// Command use-tools defines one typed tool and one raw tool, wraps both
// in a guard chain, and runs the loop until the model stops.
//
// Set ANTHROPIC_API_KEY before you run it.
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/anthropic"
	"github.com/sonnes/gopi/pkg/tool"
)

// WeatherIn is the input of the weather tool. The json tag names the
// field for the model. The jsonschema tag describes it.
type WeatherIn struct {
	City string `json:"city" jsonschema:"the city to look up"`
}

// WeatherOut is the output. tool.New encodes it as JSON text.
type WeatherOut struct {
	City  string `json:"city"`
	TempC int    `json:"temp_c"`
}

// weather is a typed tool. tool.New derives the schema from WeatherIn
// and turns an error from the function into an error result.
var weather = tool.New(
	"weather",
	func(ctx context.Context, in WeatherIn) (WeatherOut, error) {
		return WeatherOut{City: in.City, TempC: 21}, nil
	},
	tool.Description("Get the current weather of a city."),
)

// clock is a raw tool. It writes its own schema and decodes nothing.
var clock = tool.Func(
	"clock",
	func(ctx context.Context, call ai.ToolCall) (ai.ToolResult, error) {
		now := time.Now().Format(time.RFC3339)
		return ai.ToolResult{Content: []ai.Content{ai.Text{Text: now}}}, nil
	},
	tool.Description("Get the current time in RFC 3339."),
	tool.InputSchema(&jsonschema.Schema{Type: "object"}),
)

// Deny returns an error result for every tool in names. The model reads
// the result and picks another tool. The wrapped tool never runs.
func Deny(names ...string) tool.Middleware {
	denied := map[string]bool{}
	for _, n := range names {
		denied[n] = true
	}
	return func(next tool.Tool) tool.Tool {
		return tool.Wrap(next, func(ctx context.Context, call ai.ToolCall) (ai.ToolResult, error) {
			if denied[call.Name] {
				return ai.ToolResult{
					Content: []ai.Content{ai.Text{Text: "tool " + call.Name + " is not allowed"}},
					IsError: true,
				}, nil
			}
			return next.Run(ctx, call)
		})
	}
}

// Retry runs the tool again on a Go error, up to n times. An error
// result passes through, because the model handles it.
func Retry(n int) tool.Middleware {
	return func(next tool.Tool) tool.Tool {
		return tool.Wrap(next, func(ctx context.Context, call ai.ToolCall) (ai.ToolResult, error) {
			var res ai.ToolResult
			var err error
			for i := 0; i <= n; i++ {
				res, err = next.Run(ctx, call)
				if err == nil || ctx.Err() != nil {
					return res, err
				}
			}
			return res, err
		})
	}
}

// Remind appends text to every result. The model reads it with the
// result, so it is a place for a rule that the model must obey.
func Remind(text string) tool.Middleware {
	return func(next tool.Tool) tool.Tool {
		return tool.Wrap(next, func(ctx context.Context, call ai.ToolCall) (ai.ToolResult, error) {
			res, err := next.Run(ctx, call)
			if err != nil {
				return res, err
			}
			res.Content = append(res.Content, ai.Text{Text: text})
			return res, nil
		})
	}
}

// Log writes one line for each call. It edits the info as well, so the
// model sees a tag in the description.
func Log(w io.Writer) tool.Middleware {
	return func(next tool.Tool) tool.Tool {
		run := func(ctx context.Context, call ai.ToolCall) (ai.ToolResult, error) {
			res, err := next.Run(ctx, call)
			fmt.Fprintf(w, "log: %s error=%v err=%v\n", call.Name, res.IsError, err)
			return res, err
		}
		return tool.Wrap(next, run, tool.Description(next.Info().Description+" (logged)"))
	}
}

// guard applies the same chain to every tool. The first middleware is
// the outermost, so Log sees the result of Deny.
func guard(t tool.Tool) tool.Tool {
	return tool.Chain(t,
		Log(os.Stdout),
		Deny("rm"),
		Remind("Do not delete files."),
		Retry(2),
	)
}

func main() {
	ctx := context.Background()

	model := ai.LanguageModel{
		Model:    anthropic.Sonnet5,
		Provider: anthropic.New(),
	}

	tools := map[string]tool.Tool{}
	var infos []ai.ToolInfo
	for _, t := range []tool.Tool{weather, clock} {
		g := guard(t)
		tools[g.Info().Name] = g
		infos = append(infos, g.Info())
	}

	messages := []ai.Message{
		ai.UserMessage("Is it warmer in Lisbon or in Oslo right now? Say the time too."),
	}

	for {
		msg, err := model.StreamText(ctx, messages, ai.Tools(infos)).Wait()
		if err != nil {
			log.Fatal(err)
		}
		messages = append(messages, *msg)

		// A refusal can hold tool calls. Do not run them.
		if msg.StopReason != ai.StopReasonToolUse {
			fmt.Println(msg.Text())
			return
		}

		for _, call := range msg.ToolCalls() {
			fmt.Printf("-> %s %s\n", call.Name, call.Input)
			messages = append(messages, runTool(ctx, tools, call))
		}
	}
}

// runTool runs one call and returns the tool result message. An unknown
// tool and a Go error are error results, so the model can correct the
// call.
func runTool(ctx context.Context, tools map[string]tool.Tool, call ai.ToolCall) ai.Message {
	t, ok := tools[call.Name]
	if !ok {
		m := ai.ToolResultMessage(call.ID, ai.Text{Text: "unknown tool " + call.Name})
		m.IsError = true
		return m
	}

	res, err := t.Run(ctx, call)
	if ctx.Err() != nil {
		// A canceled context stops the run.
		log.Fatal(ctx.Err())
	}
	if err != nil {
		// Any other Go error goes to the model as an error result, as
		// the agent loop does.
		res = ai.ToolResult{Content: []ai.Content{ai.Text{Text: err.Error()}}, IsError: true}
	}

	m := ai.ToolResultMessage(call.ID, res.Content...)
	m.IsError = res.IsError
	return m
}
