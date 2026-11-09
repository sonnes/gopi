package ai_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

func TestGetOption_ToolChoice(t *testing.T) {
	opts := ai.JoinOptions(
		ai.ToolChoice(ai.ToolChoiceAny),
		ai.ToolChoice(ai.ToolChoiceTool("read")),
	)

	got, ok := ai.GetOption(opts, ai.ToolChoice)
	assert.True(t, ok)
	assert.Equal(t, ai.ToolChoiceMode{Type: "tool", Name: "read"}, got)
}

func TestGetOption_CallSettings(t *testing.T) {
	opts := ai.JoinOptions(
		ai.MaxRetries(3),
		ai.MaxRetryDelay(time.Second),
		ai.Temperature(0.2),
		ai.Headers(map[string]string{"x-team": "infra"}),
		ai.CacheRetention(ai.CacheLong),
	)

	retries, _ := ai.GetOption(opts, ai.MaxRetries)
	delay, _ := ai.GetOption(opts, ai.MaxRetryDelay)
	temperature, _ := ai.GetOption(opts, ai.Temperature)
	headers, _ := ai.GetOption(opts, ai.Headers)
	retention, _ := ai.GetOption(opts, ai.CacheRetention)

	assert.Equal(t, 3, retries)
	assert.Equal(t, time.Second, delay)
	assert.InDelta(t, 0.2, temperature, 1e-9)
	assert.Equal(t, map[string]string{"x-team": "infra"}, headers)
	assert.Equal(t, ai.CacheLong, retention)
}

func TestParseThinking(t *testing.T) {
	tests := []struct {
		in      string
		want    ai.ThinkingLevel
		wantErr string
	}{
		{in: "high", want: ai.ThinkingHigh},
		{in: "off", want: ai.ThinkingOff},
		{in: "max", want: ai.ThinkingMax},
		{in: "huge", wantErr: `unknown thinking level "huge"`},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ai.ParseThinking(tt.in)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMaxTokensOf(t *testing.T) {
	capped := ai.Model{ID: "m", MaxTokens: 100}
	tests := []struct {
		name   string
		model  ai.Model
		opts   ai.Options
		want   int
		wantOK bool
	}{
		{name: "the option wins", model: capped, opts: ai.MaxTokens(10), want: 10, wantOK: true},
		{name: "the cap of the model", model: capped, want: 100, wantOK: true},
		{name: "neither", model: ai.Model{ID: "m"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ai.MaxTokensOf(tt.model, tt.opts)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRetentionOf(t *testing.T) {
	assert.Equal(t, ai.CacheShort, ai.RetentionOf(nil))
	assert.Equal(t, ai.CacheNone, ai.RetentionOf(ai.CacheRetention(ai.CacheNone)))
}

func TestThinkingOf(t *testing.T) {
	level, ok, err := ai.ThinkingOf(ai.Thinking(ai.ThinkingXHigh))
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, ai.ThinkingXHigh, level)

	_, ok, err = ai.ThinkingOf(nil)
	require.NoError(t, err)
	assert.False(t, ok)

	_, _, err = ai.ThinkingOf(ai.Thinking("huge"))
	require.ErrorContains(t, err, `unknown thinking level "huge"`)
}

func TestThinkingLevel_Effort(t *testing.T) {
	var efforts []string
	for _, level := range ai.ThinkingLevels() {
		efforts = append(efforts, level.Effort())
	}
	assert.Equal(t, []string{"none", "low", "medium", "high", "xhigh", "max"}, efforts)
}

func TestToolChoiceOf(t *testing.T) {
	choice, ok, err := ai.ToolChoiceOf(ai.ToolChoice(ai.ToolChoiceTool("read")))
	require.NoError(t, err)
	assert.True(t, ok)
	assert.True(t, choice.Named())

	_, ok, err = ai.ToolChoiceOf(nil)
	require.NoError(t, err)
	assert.False(t, ok)

	_, _, err = ai.ToolChoiceOf(ai.ToolChoice(ai.ToolChoiceMode{Type: "some"}))
	require.ErrorContains(t, err, `unknown tool choice "some"`)
}

func TestSystemPromptOf(t *testing.T) {
	opts := ai.SystemPrompt([]ai.Content{
		ai.Text{Text: "Be brief."},
		ai.File{MediaType: "image/png"},
		ai.Section{Name: "rules", Text: "Cite."},
	})
	assert.Equal(t, []string{"Be brief.", "Cite."}, ai.SystemPromptOf(opts))
}
