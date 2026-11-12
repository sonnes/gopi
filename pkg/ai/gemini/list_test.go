package gemini_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/gemini"
)

const modelsBody = `{"models": [
	{
		"name": "models/gemini-3.8-flash",
		"displayName": "Gemini 3.8 Flash",
		"inputTokenLimit": 1048576,
		"outputTokenLimit": 65536,
		"supportedGenerationMethods": ["generateContent", "countTokens"],
		"thinking": true
	},
	{
		"name": "models/text-embedding-004",
		"displayName": "Text Embedding 004",
		"supportedGenerationMethods": ["embedContent"]
	}
]}`

func TestListModels(t *testing.T) {
	p, _ := fakeProvider(t, answer(modelsBody))

	models, err := p.ListModels(t.Context())
	require.NoError(t, err)

	assert.Equal(t, []ai.Model{
		{
			ID:            "gemini-3.8-flash",
			Name:          "Gemini 3.8 Flash",
			MaxTokens:     65536,
			ContextWindow: 1048576,
			Reasoning:     true,
		},
	}, models)
}

func TestCheckKey(t *testing.T) {
	t.Run("accepted", func(t *testing.T) {
		p, _ := fakeProvider(t, answer(modelsBody))

		info, err := p.CheckKey(t.Context())
		require.NoError(t, err)
		assert.Equal(t, ai.KeyInfo{}, info)
	})

	t.Run("refused", func(t *testing.T) {
		p, _ := fakeProvider(t, errorAnswer(http.StatusBadRequest, objectErrorBody))

		_, err := p.CheckKey(t.Context())

		var e *gemini.Error
		require.ErrorAs(t, err, &e)
		assert.Equal(t, http.StatusBadRequest, e.StatusCode)
		assert.Equal(t, "API key not valid.", e.Message)
	})
}
