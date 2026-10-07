// Package providertest holds the cases that every text provider must
// pass. A provider package runs them with [Run], and each case records
// and replays its traffic like the other provider tests.
package providertest

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/tool"
)

// Suite is the setup of one provider for the cases.
type Suite struct {
	// New returns the provider for the running test. It skips the test
	// if the test has no recording and no key.
	New func(t *testing.T) ai.TextProvider
	// Model is a cheap model that reads images.
	Model ai.Model
	// SystemModel takes a system message in the conversation and tool
	// changes in place.
	SystemModel ai.Model
	// ReminderModel takes no system message in the conversation, so it
	// gets a system-reminder.
	ReminderModel ai.Model
}

// Run runs every case as a subtest of t.
func Run(t *testing.T, s Suite) {
	t.Run("TwoTurns", s.twoTurns)
	t.Run("MathToolCall", s.mathToolCall)
	t.Run("ImageInput", s.imageInput)
	t.Run("EmptyContent", s.emptyContent)
	t.Run("ToolResultImage", s.toolResultImage)
	t.Run("ToolCallWithoutResult", s.toolCallWithoutResult)
	t.Run("UnicodeToolResults", s.unicodeToolResults)
	t.Run("SystemMessage", s.systemMessage)
	t.Run("ToolAdded", s.toolAdded)
}

type mathInput struct {
	A         float64 `json:"a" jsonschema:"The first number"`
	B         float64 `json:"b" jsonschema:"The second number"`
	Operation string  `json:"operation" jsonschema:"One of add, subtract, multiply, divide"`
}

// MathTool does one arithmetic operation. A provider package uses it in
// its own cases too.
var MathTool = tool.New("math_operation", func(_ context.Context, in mathInput) (string, error) {
	switch in.Operation {
	case "add":
		return fmt.Sprint(in.A + in.B), nil
	case "subtract":
		return fmt.Sprint(in.A - in.B), nil
	case "multiply":
		return fmt.Sprint(in.A * in.B), nil
	case "divide":
		return fmt.Sprint(in.A / in.B), nil
	default:
		return "", fmt.Errorf("unknown operation %q", in.Operation)
	}
}, tool.Description("Perform a basic arithmetic operation"))

type weatherInput struct {
	City string `json:"city" jsonschema:"The city name"`
}

var weatherTool = tool.New("get_weather", func(_ context.Context, in weatherInput) (string, error) {
	return "Sunny, 22C in " + in.City, nil
}, tool.Description("Get the current weather in a city"))

type noInput struct{}

//go:embed testdata/red-circle.png
var redCirclePNG []byte

// RedCircle is an image of a red circle.
func RedCircle() ai.File {
	return ai.File{MediaType: "image/png", Data: redCirclePNG}
}

// redCircle keeps the shape of the cases that read the image.
func redCircle(*testing.T) ai.File {
	return RedCircle()
}

// run runs one call and returns the final message.
func run(t *testing.T, p ai.TextProvider, model ai.Model, messages []ai.Message, opts ...ai.Options) (*ai.Message, error) {
	t.Helper()
	return p.StreamText(t.Context(), model, messages, ai.JoinOptions(opts...)).Wait()
}

func (s Suite) twoTurns(t *testing.T) {
	p := s.New(t)
	system := ai.SystemPrompt([]ai.Content{ai.Text{Text: "You are a helpful assistant. Be concise."}})

	messages := []ai.Message{ai.UserMessage("Reply with exactly: 'Hello test successful'")}
	first, err := run(t, p, s.Model, messages, system)
	require.NoError(t, err)
	assert.Equal(t, ai.RoleAssistant, first.Role)
	assert.Positive(t, first.Usage.Input+first.Usage.CacheRead)
	assert.Positive(t, first.Usage.Output)
	assert.Contains(t, first.Text(), "Hello test successful")

	messages = append(messages, *first, ai.UserMessage("Now say 'Goodbye test successful'"))
	second, err := run(t, p, s.Model, messages, system)
	require.NoError(t, err)
	assert.Contains(t, second.Text(), "Goodbye test successful")
}

func (s Suite) mathToolCall(t *testing.T) {
	p := s.New(t)

	stream := p.StreamText(
		t.Context(),
		s.Model,
		[]ai.Message{ai.UserMessage("Calculate 15 + 27 using the math_operation tool.")},
		ai.Tools([]ai.ToolInfo{MathTool.Info()}),
	)
	var types []ai.EventType
	for ev, err := range stream.Events() {
		require.NoError(t, err)
		types = append(types, ev.Type)
	}
	msg, err := stream.Wait()
	require.NoError(t, err)

	assert.Contains(t, types, ai.EventToolStart)
	assert.Contains(t, types, ai.EventToolDelta)
	assert.Contains(t, types, ai.EventToolEnd)
	assert.Equal(t, ai.StopReasonToolUse, msg.StopReason)

	calls := msg.ToolCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "math_operation", calls[0].Name)
	assert.NotEmpty(t, calls[0].ID)

	var in mathInput
	require.NoError(t, json.Unmarshal(calls[0].Input, &in))
	assert.InDelta(t, 15, in.A, 0)
	assert.InDelta(t, 27, in.B, 0)
	assert.Equal(t, "add", in.Operation)
}

func (s Suite) imageInput(t *testing.T) {
	p := s.New(t)

	prompt := "What do you see in this image? Please describe the shape (circle, rectangle, " +
		"square, triangle, ...) and color (red, blue, green, ...). You MUST reply in English."
	msg, err := run(
		t,
		p,
		s.Model,
		[]ai.Message{{
			Role:    ai.RoleUser,
			Content: []ai.Content{ai.Text{Text: prompt}, redCircle(t)},
		}},
		ai.SystemPrompt([]ai.Content{ai.Text{Text: "You are a helpful assistant."}}),
	)
	require.NoError(t, err)

	text := strings.ToLower(msg.Text())
	assert.Contains(t, text, "red")
	assert.Contains(t, text, "circle")
}

func (s Suite) emptyContent(t *testing.T) {
	tests := []struct {
		name     string
		messages []ai.Message
	}{
		{
			name:     "empty content",
			messages: []ai.Message{{Role: ai.RoleUser, Content: []ai.Content{}}},
		},
		{
			name:     "empty string",
			messages: []ai.Message{ai.UserMessage("")},
		},
		{
			name:     "whitespace",
			messages: []ai.Message{ai.UserMessage("   \n\t  ")},
		},
		{
			name: "empty assistant message",
			messages: []ai.Message{
				ai.UserMessage("Hello, how are you?"),
				ai.AssistantMessage(),
				ai.UserMessage("Please respond this time."),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := s.New(t)

			msg, err := run(t, p, s.Model, tt.messages)
			if err != nil {
				return
			}
			assert.NotEmpty(t, msg.Content, "with no error, the model answers")
		})
	}
}

func (s Suite) toolResultImage(t *testing.T) {
	tests := []struct {
		name   string
		tool   string
		prompt string
		result func(t *testing.T) []ai.Content
		want   *regexp.Regexp
	}{
		{
			name:   "only an image",
			tool:   "get_circle",
			prompt: "Call the get_circle tool to get an image, and describe what you see, shapes, colors, etc.",
			result: func(t *testing.T) []ai.Content { return []ai.Content{redCircle(t)} },
		},
		{
			name: "text and an image",
			tool: "get_circle_with_description",
			prompt: "Use the get_circle_with_description tool and tell me what you learned. " +
				"Also say what color the shape is.",
			result: func(t *testing.T) []ai.Content {
				return []ai.Content{
					ai.Text{Text: "This is a geometric shape with specific properties: it has a diameter of 100 pixels."},
					redCircle(t),
				}
			},
			want: regexp.MustCompile(`diameter|100|pixel`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := s.New(t)
			circle := tool.New(tt.tool, func(context.Context, noInput) (string, error) {
				return "", nil
			}, tool.Description("Returns a circle image"))
			opts := ai.JoinOptions(
				ai.SystemPrompt([]ai.Content{ai.Text{Text: "You are a helpful assistant that uses tools when asked."}}),
				ai.Tools([]ai.ToolInfo{circle.Info()}),
			)

			messages := []ai.Message{ai.UserMessage(tt.prompt)}
			first, err := run(t, p, s.Model, messages, opts)
			require.NoError(t, err)
			require.Equal(t, ai.StopReasonToolUse, first.StopReason)
			calls := first.ToolCalls()
			require.Len(t, calls, 1)
			assert.Equal(t, tt.tool, calls[0].Name)

			messages = append(messages, *first, ai.ToolResultMessage(calls[0].ID, tt.result(t)...))
			second, err := run(t, p, s.Model, messages, opts)
			require.NoError(t, err)
			assert.Equal(t, ai.StopReasonStop, second.StopReason)

			text := strings.ToLower(second.Text())
			assert.Contains(t, text, "red")
			assert.Contains(t, text, "circle")
			if tt.want != nil {
				assert.Regexp(t, tt.want, text)
			}
		})
	}
}

func (s Suite) toolCallWithoutResult(t *testing.T) {
	p := s.New(t)
	type calcInput struct {
		Expression string `json:"expression" jsonschema:"The mathematical expression to evaluate"`
	}
	calc := tool.New("calculate", func(context.Context, calcInput) (string, error) {
		return "", nil
	}, tool.Description("Evaluate a mathematical expression"))
	opts := ai.JoinOptions(
		ai.SystemPrompt([]ai.Content{ai.Text{Text: "You are a helpful assistant. Use the calculate tool when asked to perform calculations."}}),
		ai.Tools([]ai.ToolInfo{calc.Info()}),
	)

	messages := []ai.Message{ai.UserMessage("Please calculate 25 * 18 using the calculate tool.")}
	first, err := run(t, p, s.Model, messages, opts)
	require.NoError(t, err)
	require.NotEmpty(t, first.ToolCalls())

	// The caller sends no result for the call.
	messages = append(messages, *first, ai.UserMessage("Never mind, just tell me what is 2+2?"))
	second, err := run(t, p, s.Model, messages, opts)
	require.NoError(t, err)
	assert.NotEmpty(t, second.Content)
	assert.Contains(t, []ai.StopReason{ai.StopReasonStop, ai.StopReasonToolUse}, second.StopReason)
}

func (s Suite) unicodeToolResults(t *testing.T) {
	emoji := `Test with emoji 🙈 and other characters:
- Thumbs up: 👍
- Heart: ❤️
- Thinking face: 🤔
- Rocket: 🚀
- Mixed text: Mario Zechner wann? Wo? Bin grad äußersr eventuninformiert 🙈
- Japanese: こんにちは
- Chinese: 你好
- Mathematical symbols: ∑∫∂√
- Special quotes: "curly" 'quotes'`
	linkedin := `Post: Hab einen "Generative KI für Nicht-Techniker" Workshop gebaut.
Comments: [{"author": "Mario Zechner", "text": "Wann? Wo? Bin grad äußersr eventuninformiert 🙈"},
{"author": "Anna", "text": "Klingt spannend! 👍"}]`
	// The bytes ED A0 BD are the UTF-8 form of a lone high surrogate,
	// which is not valid UTF-8.
	surrogate := "Text with unpaired surrogate: \xed\xa0\xbd <- should be sanitized"

	tests := []struct {
		name     string
		tool     string
		result   string
		followUp string
	}{
		{name: "emoji", tool: "test_tool", result: emoji, followUp: "Summarize the tool result briefly."},
		{name: "linkedin", tool: "linkedin_skill", result: linkedin, followUp: "How many comments are there?"},
		{name: "lone surrogate", tool: "test_tool", result: surrogate, followUp: "What did the tool return?"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := s.New(t)
			info := tool.New(tt.tool, func(context.Context, noInput) (string, error) {
				return "", nil
			}, tool.Description("A test tool")).Info()

			messages := []ai.Message{
				ai.UserMessage("Use the test tool"),
				ai.AssistantMessage(ai.ToolCall{ID: "test_1", Name: tt.tool, Input: json.RawMessage(`{}`)}),
				ai.ToolResultMessage("test_1", ai.Text{Text: tt.result}),
				ai.UserMessage(tt.followUp),
			}
			msg, err := run(
				t,
				p,
				s.Model,
				messages,
				ai.SystemPrompt([]ai.Content{ai.Text{Text: "You are a helpful assistant."}}),
				ai.Tools([]ai.ToolInfo{info}),
			)
			require.NoError(t, err)
			assert.NotEmpty(t, msg.Text())
		})
	}
}

func (s Suite) systemMessage(t *testing.T) {
	tests := []struct {
		name  string
		model ai.Model
	}{
		{name: "in place", model: s.SystemModel},
		{name: "as a reminder", model: s.ReminderModel},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := s.New(t)

			messages := []ai.Message{
				ai.UserMessage("Say hello in one short sentence."),
				ai.SystemMessage(ai.Section{Name: "language", Text: "Answer only in French."}),
			}
			msg, err := run(
				t,
				p,
				tt.model,
				messages,
				ai.SystemPrompt([]ai.Content{ai.Text{Text: "You are a helpful assistant."}}),
			)
			require.NoError(t, err)

			text := strings.ToLower(msg.Text())
			isFrench := strings.Contains(text, "bonjour") || strings.Contains(text, "salut")
			assert.True(t, isFrench, "response %q is not French", msg.Text())
		})
	}
}

func (s Suite) toolAdded(t *testing.T) {
	p := s.New(t)

	messages := []ai.Message{
		ai.UserMessage("Hi"),
		ai.AssistantMessage(ai.Text{Text: "Hello! How can I help?"}),
		ai.SystemMessage(ai.ToolAdded{Tool: MathTool.Info()}),
		ai.UserMessage("Calculate 15 + 27 using the math_operation tool."),
	}
	msg, err := run(
		t,
		p,
		s.SystemModel,
		messages,
		ai.Tools([]ai.ToolInfo{weatherTool.Info()}),
	)
	require.NoError(t, err)

	calls := msg.ToolCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "math_operation", calls[0].Name, "the model can call the added tool")
}
