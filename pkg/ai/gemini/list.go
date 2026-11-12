package gemini

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"google.golang.org/genai"

	"github.com/sonnes/gopi/pkg/ai"
)

var (
	_ ai.ModelLister = (*Provider)(nil)
	_ ai.KeyChecker  = (*Provider)(nil)
)

// listPageSize is the number of models in one page of the model list.
// It is the largest size that the API takes, so a list is one call.
const listPageSize = 1000

const modelPrefix = "models/"

// chatMethod is the generation method of a model that answers a
// conversation. The list also holds embedding and other models.
const chatMethod = "generateContent"

// ListModels lists the models that answer a conversation, with the
// display name and the limits of each one. The list has no prices and
// no input kinds, so a listed model has none.
func (p *Provider) ListModels(ctx context.Context) ([]ai.Model, error) {
	if p.err != nil {
		return nil, p.err
	}
	config := &genai.ListModelsConfig{PageSize: listPageSize}
	page, err := p.client.Models.List(ctx, config)

	var models []ai.Model
	for err == nil {
		for _, m := range page.Items {
			if slices.Contains(m.SupportedActions, chatMethod) {
				models = append(models, listedModel(m))
			}
		}
		page, err = page.Next(ctx)
	}
	if !errors.Is(err, genai.ErrPageDone) {
		return nil, fmt.Errorf("gemini: list models: %w", apiError(err))
	}
	return models, nil
}

func listedModel(m *genai.Model) ai.Model {
	return ai.Model{
		ID:            strings.TrimPrefix(m.Name, modelPrefix),
		Name:          m.DisplayName,
		MaxTokens:     int(m.OutputTokenLimit),
		ContextWindow: int(m.InputTokenLimit),
		Reasoning:     m.Thinking,
	}
}

// CheckKey asks for one page of one model, so a refused key fails and
// no model runs. The API says nothing more about the key, so the info
// is zero.
func (p *Provider) CheckKey(ctx context.Context) (ai.KeyInfo, error) {
	if p.err != nil {
		return ai.KeyInfo{}, p.err
	}
	config := &genai.ListModelsConfig{PageSize: 1}
	if _, err := p.client.Models.List(ctx, config); err != nil {
		return ai.KeyInfo{}, fmt.Errorf("gemini: check key: %w", apiError(err))
	}
	return ai.KeyInfo{}, nil
}
