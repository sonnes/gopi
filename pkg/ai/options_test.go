package ai_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sonnes/gopi/pkg/ai"
)

// Region is an option that another package defines. The ai package
// never sees its type, and it travels in the same list.
var Region = ai.NewOption[string]()

func TestGetOption_LaterValueWins(t *testing.T) {
	opts := ai.JoinOptions(ai.MaxTokens(1), ai.Thinking(ai.ThinkingLow), ai.MaxTokens(2))

	n, ok := ai.GetOption(opts, ai.MaxTokens)
	assert.True(t, ok)
	assert.Equal(t, 2, n)

	level, ok := ai.GetOption(opts, ai.Thinking)
	assert.True(t, ok)
	assert.Equal(t, ai.ThinkingLow, level)
}

func TestGetOption_NestedJoin(t *testing.T) {
	inner := ai.JoinOptions(ai.MaxTokens(1), ai.ImageCount(4))
	opts := ai.JoinOptions(inner, ai.MaxTokens(2))

	n, ok := ai.GetOption(opts, ai.MaxTokens)
	assert.True(t, ok)
	assert.Equal(t, 2, n)

	count, ok := ai.GetOption(opts, ai.ImageCount)
	assert.True(t, ok)
	assert.Equal(t, 4, count)
}

func TestGetOption_UnsetVersusZero(t *testing.T) {
	_, ok := ai.GetOption(ai.JoinOptions(ai.ImageSize("1024x1024")), ai.MaxTokens)
	assert.False(t, ok, "unset")

	n, ok := ai.GetOption(ai.MaxTokens(0), ai.MaxTokens)
	assert.True(t, ok, "set to zero")
	assert.Equal(t, 0, n)
}

func TestGetOption_Nil(t *testing.T) {
	n, ok := ai.GetOption(nil, ai.MaxTokens)
	assert.False(t, ok)
	assert.Equal(t, 0, n)

	_, ok = ai.GetOption(ai.JoinOptions(), ai.MaxTokens)
	assert.False(t, ok)
}

func TestGetOption_ForeignOption(t *testing.T) {
	opts := ai.JoinOptions(ai.MaxTokens(1), Region("eu"))

	region, ok := ai.GetOption(opts, Region)
	assert.True(t, ok)
	assert.Equal(t, "eu", region)

	// Two options with the same value type stay distinct.
	Other := ai.NewOption[string]()
	_, ok = ai.GetOption(opts, Other)
	assert.False(t, ok)
}

func TestGetOption_SystemPrompt(t *testing.T) {
	rules := ai.Section{Name: "rules", Text: "<rules>Cite.</rules>"}
	opts := ai.JoinOptions(
		ai.SystemPrompt([]ai.Content{ai.Text{Text: "Be brief."}}),
		ai.SystemPrompt([]ai.Content{ai.Text{Text: "Be concise."}, rules}),
	)

	got, ok := ai.GetOption(opts, ai.SystemPrompt)
	assert.True(t, ok)
	assert.Equal(t, []ai.Content{ai.Text{Text: "Be concise."}, rules}, got, "a later value wins")

	_, ok = ai.GetOption(ai.MaxTokens(1), ai.SystemPrompt)
	assert.False(t, ok, "unset")
}

func TestGetOption_Tools(t *testing.T) {
	tools := []ai.ToolInfo{{Name: "weather"}}
	opts := ai.JoinOptions(ai.MaxTokens(1), ai.Tools(tools))

	got, ok := ai.GetOption(opts, ai.Tools)
	assert.True(t, ok)
	assert.Equal(t, tools, got)

	_, ok = ai.GetOption(ai.MaxTokens(1), ai.Tools)
	assert.False(t, ok, "unset")
}
