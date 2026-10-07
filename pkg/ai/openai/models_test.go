package openai_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/openai"
)

func TestModels(t *testing.T) {
	got := openai.Models()

	// The default text model comes first. The image models come last.
	assert.Equal(t, []ai.Model{
		openai.GPT6Sol,
		openai.GPT6Luna,
		openai.GPT6Astra,
		openai.GPT5_6,
		openai.GPTImage2,
		openai.GPTImage2_5Flare,
		openai.GPTImage2_5Sunburst,
	}, got)
}
