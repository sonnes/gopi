package openai_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/openai"
	"github.com/sonnes/gopi/pkg/catalog/models"
)

func TestModels(t *testing.T) {
	got := openai.Models()

	assert.Equal(t, openai.GPT6Sol, got[0], "the default text model comes first")
	assert.ElementsMatch(t, []ai.Model{
		openai.GPT6Sol,
		openai.GPT6Luna,
		openai.GPT6Astra,
		openai.GPT5_6,
		openai.GPTImage2,
		openai.GPTImage2_5Flare,
		openai.GPTImage2_5Sunburst,
	}, got)
	assert.Equal(t, models.Of("openai"), got, "the models come from models.json")
}
