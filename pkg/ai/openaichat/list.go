package openaichat

import (
	"context"
	"errors"
	"fmt"

	"github.com/sonnes/gopi/pkg/ai"
)

var _ ai.ModelLister = (*Provider)(nil)

// ListModels lists the models of GET /models through the provider of
// package openai, because both APIs serve the same list. OpenAI sends
// only the ID of each model. The name comes from a compatible server
// that sends one, and the output cap stays zero. An API error wraps
// [Error].
func (p *Provider) ListModels(ctx context.Context) ([]ai.Model, error) {
	models, err := p.models.ListModels(ctx)
	if err != nil {
		// The error of the openai provider starts with its own prefix.
		// Its cause is the error of the SDK.
		cause := errors.Unwrap(err)
		if cause == nil {
			cause = err
		}
		return nil, fmt.Errorf("openaichat: list models: %w", cause)
	}
	return models, nil
}
