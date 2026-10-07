package ai_test

import (
	"context"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
)

var testModel = ai.Model{ID: "test-model", Name: "Test"}

// textProvider implements only [ai.TextProvider]. It records what it
// received so a test can check the binding.
type textProvider struct {
	model    ai.Model
	messages []ai.Message
	opts     ai.Options
}

func (p *textProvider) StreamText(
	_ context.Context,
	model ai.Model,
	messages []ai.Message,
	opts ai.Options,
) *ai.EventStream {
	p.model = model
	p.messages = messages
	p.opts = opts
	return ai.NewEventStream(func(push func(ai.Event)) (*ai.Message, error) {
		msg := ai.AssistantMessage(ai.Text{Text: "ok"})
		return &msg, nil
	})
}

// objectProvider implements [ai.TextProvider] and [ai.ObjectProvider].
type objectProvider struct {
	textProvider
	schema *jsonschema.Schema
}

func (p *objectProvider) GenerateObject(
	_ context.Context,
	model ai.Model,
	_ []ai.Message,
	schema *jsonschema.Schema,
	opts ai.Options,
) (*ai.ObjectResponse, error) {
	p.model = model
	p.opts = opts
	p.schema = schema
	return &ai.ObjectResponse{Raw: `{"name":"Alice","age":30}`, Usage: ai.Usage{Input: 1}}, nil
}

// tokenProvider implements [ai.TextProvider] and [ai.TokenCounter].
type tokenProvider struct {
	textProvider
}

func (p *tokenProvider) CountTokens(
	_ context.Context,
	model ai.Model,
	_ []ai.Message,
	opts ai.Options,
) (*ai.TokenCount, error) {
	p.model = model
	p.opts = opts
	return &ai.TokenCount{Total: 42}, nil
}

type person struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

func TestLanguageModel_StreamText(t *testing.T) {
	p := &textProvider{}
	lm := ai.LanguageModel{Model: testModel, Provider: p}

	messages := []ai.Message{ai.UserMessage("hi")}
	msg, err := lm.StreamText(
		t.Context(),
		messages,
		ai.MaxTokens(1),
		ai.MaxTokens(7),
	).Wait()
	require.NoError(t, err)
	assert.Equal(t, "ok", msg.Text())
	assert.Equal(t, testModel, p.model)
	assert.Equal(t, messages, p.messages)

	n, ok := ai.GetOption(p.opts, ai.MaxTokens)
	assert.True(t, ok)
	assert.Equal(t, 7, n)
}

func TestGenerateObject(t *testing.T) {
	p := &objectProvider{}
	lm := ai.LanguageModel{Model: testModel, Provider: p}

	res, err := ai.GenerateObject[person](t.Context(), lm, nil, ai.MaxTokens(3))
	require.NoError(t, err)
	assert.Equal(t, person{Name: "Alice", Age: 30}, res.Object)
	assert.Equal(t, `{"name":"Alice","age":30}`, res.Raw)
	assert.Equal(t, ai.Usage{Input: 1}, res.Usage)
	assert.Equal(t, testModel, p.model)
	require.NotNil(t, p.schema)
	assert.Contains(t, p.schema.Properties, "name")

	n, ok := ai.GetOption(p.opts, ai.MaxTokens)
	assert.True(t, ok)
	assert.Equal(t, 3, n)
}

func TestGenerateObject_Unsupported(t *testing.T) {
	lm := ai.LanguageModel{Model: testModel, Provider: &textProvider{}}
	_, err := ai.GenerateObject[person](t.Context(), lm, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "object")
}

func TestCountTokens(t *testing.T) {
	p := &tokenProvider{}
	lm := ai.LanguageModel{Model: testModel, Provider: p}

	count, err := ai.CountTokens(t.Context(), lm, nil, ai.Thinking(ai.ThinkingHigh))
	require.NoError(t, err)
	assert.Equal(t, 42, count.Total)
	assert.Equal(t, testModel, p.model)

	level, ok := ai.GetOption(p.opts, ai.Thinking)
	assert.True(t, ok)
	assert.Equal(t, ai.ThinkingHigh, level)
}

func TestCountTokens_Unsupported(t *testing.T) {
	lm := ai.LanguageModel{Model: testModel, Provider: &textProvider{}}
	_, err := ai.CountTokens(t.Context(), lm, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "token")
}

type imageProvider struct {
	model  ai.Model
	prompt string
	opts   ai.Options
}

func (p *imageProvider) GenerateImage(
	_ context.Context,
	model ai.Model,
	prompt string,
	opts ai.Options,
) (*ai.ImageResponse, error) {
	p.model = model
	p.prompt = prompt
	p.opts = opts
	return &ai.ImageResponse{Images: []ai.GeneratedImage{{MediaType: "image/png"}}}, nil
}

func TestImageModel_GenerateImage(t *testing.T) {
	p := &imageProvider{}
	im := ai.ImageModel{Model: testModel, Provider: p}

	res, err := im.GenerateImage(t.Context(), "a cat", ai.ImageSize("1024x1024"))
	require.NoError(t, err)
	assert.Equal(t, "image/png", res.Images[0].MediaType)
	assert.Equal(t, testModel, p.model)
	assert.Equal(t, "a cat", p.prompt)

	size, ok := ai.GetOption(p.opts, ai.ImageSize)
	assert.True(t, ok)
	assert.Equal(t, "1024x1024", size)
}

type speechProvider struct {
	model ai.Model
	text  string
}

func (p *speechProvider) GenerateSpeech(
	_ context.Context,
	model ai.Model,
	text string,
	_ ai.Options,
) (*ai.SpeechResponse, error) {
	p.model = model
	p.text = text
	return &ai.SpeechResponse{Audio: []byte("wav"), MediaType: "audio/wav"}, nil
}

func TestSpeechModel_GenerateSpeech(t *testing.T) {
	p := &speechProvider{}
	sm := ai.SpeechModel{Model: testModel, Provider: p}

	res, err := sm.GenerateSpeech(t.Context(), "hello")
	require.NoError(t, err)
	assert.Equal(t, "audio/wav", res.MediaType)
	assert.Equal(t, testModel, p.model)
	assert.Equal(t, "hello", p.text)
}
