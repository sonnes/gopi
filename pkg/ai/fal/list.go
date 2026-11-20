package fal

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/sonnes/gopi/pkg/ai"
)

// pricingRoute is the route of the prices of the platform API. It needs
// a key, and it runs no model.
const pricingRoute = "/v1/models/pricing"

// ListModels returns the fixed list of text-to-image endpoints. It does
// not call the API, because fal lists more than a hundred image
// endpoints with different inputs.
func (p *Provider) ListModels(context.Context) ([]ai.Model, error) {
	models := make([]ai.Model, 0, len(endpoints))
	for _, e := range endpoints {
		models = append(models, ai.Model{
			ID:     e.id,
			Name:   e.name,
			Input:  []ai.Modality{ai.ModalityText},
			Output: []ai.Output{ai.OutputImage},
		})
	}
	return models, nil
}

// CheckKey checks the key with a call to the pricing API for the first
// model of the list. The call runs no model and costs nothing. The API
// gives no facts about a key, so the info is zero.
func (p *Provider) CheckKey(ctx context.Context) (ai.KeyInfo, error) {
	query := url.Values{"endpoint_id": {endpoints[0].id}}
	target := p.cfg.apiBaseURL + pricingRoute + "?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return ai.KeyInfo{}, fmt.Errorf("fal: key check: %w", err)
	}
	if _, err := p.do(req, nil); err != nil {
		return ai.KeyInfo{}, fmt.Errorf("fal: key check: %w", err)
	}
	return ai.KeyInfo{}, nil
}
