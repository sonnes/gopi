package openai

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sonnes/gopi/pkg/ai"
)

var _ ai.ModelLister = (*Provider)(nil)

// listedModel holds the fields of a listed model that the SDK type
// drops. OpenAI sends no name, and a compatible server such as
// OpenRouter does.
type listedModel struct {
	Name string `json:"name"`
}

// ListModels lists the models of GET /models. OpenAI sends only the ID
// of each model. The name comes from a compatible server that sends one,
// and the output cap stays zero.
func (p *Provider) ListModels(ctx context.Context) ([]ai.Model, error) {
	pager := p.client.Models.ListAutoPaging(ctx)

	var models []ai.Model
	for pager.Next() {
		m := pager.Current()
		var extra listedModel
		// A body with no name, or one that does not decode, gives no name.
		_ = json.Unmarshal([]byte(m.RawJSON()), &extra)
		models = append(models, ai.Model{
			ID:   m.ID,
			Name: extra.Name,
		})
	}
	if err := pager.Err(); err != nil {
		return nil, fmt.Errorf("openai: list models: %w", err)
	}
	return models, nil
}

var _ ai.KeyChecker = (*Provider)(nil)

// CheckKey asks for the first page of GET /models, so a refused key
// fails and no model runs. OpenAI says nothing more about the key, so
// the info is zero.
func (p *Provider) CheckKey(ctx context.Context) (ai.KeyInfo, error) {
	if _, err := p.client.Models.List(ctx); err != nil {
		return ai.KeyInfo{}, fmt.Errorf("openai: check key: %w", err)
	}
	return ai.KeyInfo{}, nil
}
