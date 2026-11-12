package gemini_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/internal/providertest"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/gemini"
)

// testModel is the recorded model. It reads images, and it thinks, so
// the cases send thought signatures back.
var testModel = ai.Model{
	ID:        "gemini-3.8-flash",
	MaxTokens: 8192,
}

// canonicalHost is the host in every recording.
const canonicalHost = "generativelanguage.googleapis.com"

// recordFrom is the API that a test records from.
var recordFrom = providertest.Server{KeyEnv: "GEMINI_API_KEY"}

// newTestProvider returns a provider that replays the recording of the
// test.
func newTestProvider(t *testing.T) *gemini.Provider {
	t.Helper()
	r := providertest.Open(t, recordFrom, canonicalHost)
	return gemini.New(
		gemini.APIKey(r.APIKey),
		gemini.HTTPClient(r.Client),
		gemini.BaseURL(r.BaseURL),
	)
}

func TestCases(t *testing.T) {
	providertest.Run(t, providertest.Suite{
		New:           func(t *testing.T) ai.TextProvider { return newTestProvider(t) },
		Model:         testModel,
		SystemModel:   testModel,
		ReminderModel: testModel,
		ObjectModel:   testModel,
		ObjectOptions: ai.Thinking(ai.ThinkingLow),
	})
}

// TestToolCallFromAnotherModel sends a history whose tool call came
// from another provider, with no thought step. A session can switch
// models in the middle, so the API must take it.
func TestToolCallFromAnotherModel(t *testing.T) {
	p := newTestProvider(t)

	messages := []ai.Message{
		ai.UserMessage("What is the weather in Paris? Use the get_weather tool."),
		ai.AssistantMessage(
			ai.ThinkingBlock{Thinking: "Call the tool.", Signature: "anthropic-signature"},
			ai.ToolCall{ID: "toolu_01", Name: "get_weather", Input: json.RawMessage(`{"city":"Paris"}`)},
		),
		ai.ToolResultMessage("toolu_01", ai.Text{Text: "Sunny, 22C in Paris"}),
	}

	msg, err := p.StreamText(t.Context(), testModel, messages, ai.Tools([]ai.ToolInfo{weatherTool})).Wait()
	require.NoError(t, err)
	assert.Equal(t, ai.StopReasonStop, msg.StopReason)
	assert.Contains(t, msg.Text(), "22")
}
