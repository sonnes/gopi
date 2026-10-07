package openaichat_test

import (
	"testing"

	"github.com/sonnes/gopi/internal/providertest"
	"github.com/sonnes/gopi/pkg/ai"
)

// reminderModel takes no system message in the conversation. The
// provider does not know its ID, so it gets a system-reminder.
var reminderModel = ai.Model{ID: "openai/gpt-5.4-mini", MaxTokens: 16_000}

func TestCases(t *testing.T) {
	providertest.Run(t, providertest.Suite{
		New:           func(t *testing.T) ai.TextProvider { return newTestProvider(t, compatible) },
		Model:         testModel,
		SystemModel:   testModel,
		ReminderModel: reminderModel,
	})
}
