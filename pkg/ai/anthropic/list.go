package anthropic

import (
	"context"
	"fmt"

	sdk "github.com/anthropics/anthropic-sdk-go"

	"github.com/sonnes/gopi/pkg/ai"
)

var _ ai.ModelLister = (*Provider)(nil)

// listPageSize is the number of models in one page of the Models API.
// It is the largest size that the API accepts, so a list is one call.
const listPageSize = 1000

// ListModels lists the models of the Models API, with the display name,
// the limits, and the capabilities of each one. The API has no prices,
// so a listed model has none.
func (p *Provider) ListModels(ctx context.Context) ([]ai.Model, error) {
	params := sdk.ModelListParams{
		Limit: sdk.Int(listPageSize),
	}
	pager := p.client.Models.ListAutoPaging(ctx, params)

	var models []ai.Model
	for pager.Next() {
		m := pager.Current()
		models = append(models, listedModel(m))
	}
	if err := pager.Err(); err != nil {
		return nil, fmt.Errorf("anthropic: list models: %w", err)
	}
	return models, nil
}

// listedModel maps one model of the Models API.
func listedModel(m sdk.ModelInfo) ai.Model {
	caps := m.Capabilities

	input := []ai.Modality{ai.ModalityText}
	if caps.ImageInput.Supported {
		input = append(input, ai.ModalityImage)
	}
	if caps.PDFInput.Supported {
		input = append(input, ai.ModalityPDF)
	}

	return ai.Model{
		ID:             m.ID,
		Name:           m.DisplayName,
		MaxTokens:      int(m.MaxTokens),
		ContextWindow:  int(m.MaxInputTokens),
		Input:          input,
		Reasoning:      caps.Thinking.Supported,
		ThinkingLevels: effortLevels(caps.Effort),
	}
}

// effortLevels returns the thinking levels of the efforts that a model
// supports. The API does not say if a model can turn thinking off, so
// the list never holds [ai.ThinkingOff].
func effortLevels(effort sdk.EffortCapability) []ai.ThinkingLevel {
	if !effort.Supported {
		return nil
	}
	supported := []struct {
		level ai.ThinkingLevel
		ok    bool
	}{
		{ai.ThinkingLow, effort.Low.Supported},
		{ai.ThinkingMedium, effort.Medium.Supported},
		{ai.ThinkingHigh, effort.High.Supported},
		{ai.ThinkingXHigh, effort.Xhigh.Supported},
		{ai.ThinkingMax, effort.Max.Supported},
	}
	var levels []ai.ThinkingLevel
	for _, s := range supported {
		if s.ok {
			levels = append(levels, s.level)
		}
	}
	return levels
}
