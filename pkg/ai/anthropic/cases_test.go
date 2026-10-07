package anthropic_test

// The cases in this file come from the test suite of pi-mono
// (packages/ai/test).

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/anthropic"
	"github.com/sonnes/gopi/pkg/tool"
)

// The models of the cases.
var (
	haiku    = anthropic.Haiku4_5
	sonnet5  = anthropic.Sonnet5
	sonnet55 = anthropic.Sonnet5_5
	fable    = anthropic.Fable5_1
)

type mathInput struct {
	A         float64 `json:"a" jsonschema:"The first number"`
	B         float64 `json:"b" jsonschema:"The second number"`
	Operation string  `json:"operation" jsonschema:"One of add, subtract, multiply, divide"`
}

var mathTool = tool.New("math_operation", func(_ context.Context, in mathInput) (string, error) {
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

type noInput struct{}

// redCircle reads the image of a red circle.
func redCircle(t *testing.T) ai.File {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "red-circle.png"))
	require.NoError(t, err)
	return ai.File{MediaType: "image/png", Data: data}
}

// stream runs one call and returns the final message.
func stream(t *testing.T, p *anthropic.Provider, model ai.Model, messages []ai.Message, opts ...ai.Options) (*ai.Message, error) {
	t.Helper()
	return p.StreamText(t.Context(), model, messages, ai.JoinOptions(opts...)).Wait()
}

func TestCase_TwoTurns(t *testing.T) {
	p := newTestProvider(t)
	system := ai.SystemPrompt([]ai.Content{ai.Text{Text: "You are a helpful assistant. Be concise."}})

	messages := []ai.Message{ai.UserMessage("Reply with exactly: 'Hello test successful'")}
	first, err := stream(t, p, haiku, messages, system)
	require.NoError(t, err)
	assert.Equal(t, ai.RoleAssistant, first.Role)
	assert.Positive(t, first.Usage.Input+first.Usage.CacheRead)
	assert.Positive(t, first.Usage.Output)
	assert.Contains(t, first.Text(), "Hello test successful")

	messages = append(messages, *first, ai.UserMessage("Now say 'Goodbye test successful'"))
	second, err := stream(t, p, haiku, messages, system)
	require.NoError(t, err)
	assert.Contains(t, second.Text(), "Goodbye test successful")
}

func TestCase_MathToolCall(t *testing.T) {
	p := newTestProvider(t)

	s := p.StreamText(
		t.Context(),
		haiku,
		[]ai.Message{ai.UserMessage("Calculate 15 + 27 using the math_operation tool.")},
		ai.Tools([]ai.ToolInfo{mathTool.Info()}),
	)
	var types []ai.EventType
	for ev, err := range s.Events() {
		require.NoError(t, err)
		types = append(types, ev.Type)
	}
	msg, err := s.Wait()
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

func TestCase_ImageInput(t *testing.T) {
	p := newTestProvider(t)

	prompt := "What do you see in this image? Please describe the shape (circle, rectangle, " +
		"square, triangle, ...) and color (red, blue, green, ...). You MUST reply in English."
	msg, err := stream(
		t,
		p,
		haiku,
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

func TestCase_EmptyContent(t *testing.T) {
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
			p := newTestProvider(t)

			msg, err := stream(t, p, haiku, tt.messages)
			if err != nil {
				return
			}
			assert.NotEmpty(t, msg.Content, "with no error, the model answers")
		})
	}
}

func TestCase_ToolResultImage(t *testing.T) {
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
			p := newTestProvider(t)
			circle := tool.New(tt.tool, func(context.Context, noInput) (string, error) {
				return "", nil
			}, tool.Description("Returns a circle image"))
			opts := ai.JoinOptions(
				ai.SystemPrompt([]ai.Content{ai.Text{Text: "You are a helpful assistant that uses tools when asked."}}),
				ai.Tools([]ai.ToolInfo{circle.Info()}),
			)

			messages := []ai.Message{ai.UserMessage(tt.prompt)}
			first, err := stream(t, p, haiku, messages, opts)
			require.NoError(t, err)
			require.Equal(t, ai.StopReasonToolUse, first.StopReason)
			calls := first.ToolCalls()
			require.Len(t, calls, 1)
			assert.Equal(t, tt.tool, calls[0].Name)

			messages = append(messages, *first, ai.ToolResultMessage(calls[0].ID, tt.result(t)...))
			second, err := stream(t, p, haiku, messages, opts)
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

func TestCase_ToolCallWithoutResult(t *testing.T) {
	p := newTestProvider(t)
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
	first, err := stream(t, p, haiku, messages, opts)
	require.NoError(t, err)
	require.NotEmpty(t, first.ToolCalls())

	// The caller sends no result for the call.
	messages = append(messages, *first, ai.UserMessage("Never mind, just tell me what is 2+2?"))
	second, err := stream(t, p, haiku, messages, opts)
	require.NoError(t, err)
	assert.NotEmpty(t, second.Content)
	assert.Contains(t, []ai.StopReason{ai.StopReasonStop, ai.StopReasonToolUse}, second.StopReason)
}

func TestCase_UnicodeToolResults(t *testing.T) {
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
			p := newTestProvider(t)
			info := tool.New(tt.tool, func(context.Context, noInput) (string, error) {
				return "", nil
			}, tool.Description("A test tool")).Info()

			messages := []ai.Message{
				ai.UserMessage("Use the test tool"),
				ai.AssistantMessage(ai.ToolCall{ID: "test_1", Name: tt.tool, Input: json.RawMessage(`{}`)}),
				ai.ToolResultMessage("test_1", ai.Text{Text: tt.result}),
				ai.UserMessage(tt.followUp),
			}
			msg, err := stream(
				t,
				p,
				haiku,
				messages,
				ai.SystemPrompt([]ai.Content{ai.Text{Text: "You are a helpful assistant."}}),
				ai.Tools([]ai.ToolInfo{info}),
			)
			require.NoError(t, err)
			assert.NotEmpty(t, msg.Text())
		})
	}
}

func TestCase_InterleavedThinking(t *testing.T) {
	p := newTestProvider(t)
	opts := ai.JoinOptions(
		ai.SystemPrompt([]ai.Content{ai.Text{Text: "You are a helpful assistant that must use tools for arithmetic. " +
			"Always think before every tool call, not just the first one. " +
			"Do not answer with plain text when a tool call is required."}}),
		ai.Tools([]ai.ToolInfo{mathTool.Info()}),
		ai.Thinking(ai.ThinkingHigh),
	)
	prompt := "Use math_operation to calculate 328 * 29. You must call the math_operation tool exactly once. " +
		"Provide the final answer based on the best guess given the tool result, even if it seems unreliable. " +
		"Start by thinking about the steps you will take to solve the problem."

	messages := []ai.Message{ai.UserMessage(prompt)}
	first, err := stream(t, p, sonnet55, messages, opts)
	require.NoError(t, err)
	require.Equal(t, ai.StopReasonToolUse, first.StopReason)
	assert.Equal(t, "high", first.Effort)

	calls := first.ToolCalls()
	require.Len(t, calls, 1)
	result, err := mathTool.Run(t.Context(), calls[0])
	require.NoError(t, err)
	answer := result.Content[0].(ai.Text).Text

	messages = append(
		messages,
		*first,
		ai.ToolResultMessage(calls[0].ID, ai.Text{Text: "The answer is " + answer + " or " + answer + answer + "."}),
	)
	second, err := stream(t, p, sonnet55, messages, opts)
	require.NoError(t, err)
	assert.Equal(t, ai.StopReasonStop, second.StopReason)
	assert.NotEmpty(t, second.Text())
	// Adaptive thinking can skip the thinking before the first call. The
	// thinking after a tool result is the interleaved thinking.
	assert.True(t, hasThinking(second), "thinking after the tool result")
}

// hasThinking reports if msg has a signed thinking block.
func hasThinking(msg *ai.Message) bool {
	for _, c := range msg.Content {
		if th, ok := c.(ai.ThinkingBlock); ok && th.Signature != "" {
			return true
		}
	}
	return false
}

func TestCase_ThinkingBinding(t *testing.T) {
	p := newTestProvider(t)

	// strict makes the API fail on a thinking block whose prefix does
	// not match, instead of a drop of the block.
	strict := anthropic.OnPayload(func(params *sdk.MessageNewParams) error {
		params.Thinking.OfAdaptive.SetExtraFields(map[string]any{
			"block_binding": map[string]any{"prefix_mismatch_behavior": "error"},
		})
		return nil
	})
	opts := func(level ai.ThinkingLevel) ai.Options {
		return ai.JoinOptions(
			ai.CacheRetention(ai.CacheNone),
			ai.MaxTokens(4096),
			ai.Thinking(level),
			strict,
		)
	}

	firstUser := ai.UserMessage("Compute 982451653 multiplied by 961748941. Return only the integer.")
	first, err := stream(t, p, fable, []ai.Message{firstUser}, opts(ai.ThinkingLow))
	require.NoError(t, err)
	require.Equal(t, ai.StopReasonStop, first.StopReason)
	require.True(t, hasThinking(first))
	assert.Equal(t, "low", first.Effort)

	secondUser := ai.UserMessage("Reply with exactly: ok")
	exact, err := stream(t, p, fable, []ai.Message{firstUser, *first, secondUser}, opts(ai.ThinkingHigh))
	require.NoError(t, err)
	assert.Equal(t, ai.StopReasonStop, exact.StopReason, "the effort of the old turn keeps its prefix")

	unmanaged := *first
	unmanaged.Effort = ""
	_, err = stream(t, p, fable, []ai.Message{firstUser, unmanaged, secondUser}, opts(ai.ThinkingHigh))
	require.Error(t, err, "without its effort, the old turn has another prefix")
	assert.Contains(t, err.Error(), "signature")
}

func TestCase_ThinkingOff(t *testing.T) {
	p := newTestProvider(t)

	s := p.StreamText(
		t.Context(),
		sonnet5,
		[]ai.Message{ai.UserMessage("Before replying, carefully solve 36863 * 5279 internally. " +
			"Then reply with the word pong repeated exactly 40 times, separated by single spaces. " +
			"Do not add any other text.")},
		ai.JoinOptions(
			ai.SystemPrompt([]ai.Content{ai.Text{Text: "You are a precise assistant. Follow the requested output format exactly."}}),
			ai.Thinking(ai.ThinkingOff),
			ai.Temperature(0),
			ai.MaxTokens(160),
		),
	)
	var thinkingEvents int
	for ev, err := range s.Events() {
		require.NoError(t, err)
		if ev.Type == ai.EventThinkStart || ev.Type == ai.EventThinkDelta {
			thinkingEvents++
		}
	}
	msg, err := s.Wait()
	require.NoError(t, err)

	assert.Zero(t, thinkingEvents)
	for _, c := range msg.Content {
		assert.IsType(t, ai.Text{}, c, "no thinking block")
	}
	pongs := regexp.MustCompile(`(?i)\bpong\b`).FindAllString(msg.Text(), -1)
	assert.GreaterOrEqual(t, len(pongs), 35)
}

func TestCase_LongCacheRetention(t *testing.T) {
	p := newTestProvider(t)

	// The prefix must pass the minimum cacheable size of the model.
	system := "You are a concise assistant. " + strings.Repeat("Answer in one short sentence. ", 1200)
	msg, err := stream(
		t,
		p,
		haiku,
		[]ai.Message{ai.UserMessage("Reply with exactly: long cache retention accepted")},
		ai.SystemPrompt([]ai.Content{ai.Text{Text: system}}),
		ai.CacheRetention(ai.CacheLong),
		ai.MaxTokens(128),
	)
	require.NoError(t, err)
	assert.Contains(t, msg.Text(), "long cache retention accepted")
	assert.Positive(t, msg.Usage.CacheWriteLong+msg.Usage.CacheRead, "the 1-hour cache holds the prefix")
}

func TestCase_SystemMessage(t *testing.T) {
	tests := []struct {
		name  string
		model ai.Model
	}{
		{name: "in place", model: sonnet55},
		{name: "as a reminder", model: sonnet5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newTestProvider(t)

			messages := []ai.Message{
				ai.UserMessage("Say hello in one short sentence."),
				ai.SystemMessage(ai.Section{Name: "language", Text: "Answer only in French."}),
			}
			msg, err := stream(
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

func TestCase_ToolAdded(t *testing.T) {
	p := newTestProvider(t)

	messages := []ai.Message{
		ai.UserMessage("Hi"),
		ai.AssistantMessage(ai.Text{Text: "Hello! How can I help?"}),
		ai.SystemMessage(ai.ToolAdded{Tool: mathTool.Info()}),
		ai.UserMessage("Calculate 15 + 27 using the math_operation tool."),
	}
	msg, err := stream(
		t,
		p,
		sonnet55,
		messages,
		ai.Tools([]ai.ToolInfo{weatherTool.Info()}),
	)
	require.NoError(t, err)

	calls := msg.ToolCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "math_operation", calls[0].Name, "the model can call the added tool")
}
