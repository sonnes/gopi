package openrouter

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sonnes/gopi/internal/httpapi"
	"github.com/sonnes/gopi/pkg/ai"
)

// keyResponse is the body of GET /key, with the fields that gopi reads.
type keyResponse struct {
	Data struct {
		Label      string   `json:"label"`
		Limit      *float64 `json:"limit"`
		Usage      float64  `json:"usage"`
		IsFreeTier bool     `json:"is_free_tier"`
	} `json:"data"`
}

// CheckKey reads the key of the provider from GET /key, with its
// credit. The list of models is public, so only this call tests the key.
func (p *Provider) CheckKey(ctx context.Context) (ai.KeyInfo, error) {
	url := p.cfg.baseURL + "/key"
	body, err := httpapi.Get(ctx, p.cfg.httpClient, url, p.cfg.apiKey)
	if err != nil {
		return ai.KeyInfo{}, fmt.Errorf("openrouter: check key: %w", err)
	}
	var resp keyResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return ai.KeyInfo{}, fmt.Errorf("openrouter: check key: %w", err)
	}
	d := resp.Data
	info := ai.KeyInfo{
		Label:    d.Label,
		Usage:    d.Usage,
		FreeTier: d.IsFreeTier,
	}
	if d.Limit != nil {
		info.Limit = *d.Limit
		info.Limited = true
	}
	return info, nil
}
