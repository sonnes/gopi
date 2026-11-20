package higgsfield

import (
	"context"

	"github.com/sonnes/gopi/pkg/ai"
)

// The model IDs. A model ID is the path of its submit endpoint, with no
// leading slash.
const (
	// ModelSoulV2 is SOUL V2, the text-to-image model with styles.
	ModelSoulV2 = "higgsfield-ai/soul/v2/standard"
	// ModelSoulCinema is SOUL Cinema, a text-to-image model with one
	// fixed cinema style.
	ModelSoulCinema = "higgsfield-ai/soul/cinema"
)

// ListModels returns SOUL V2 and SOUL Cinema. The API has no models
// endpoint, so the list is fixed. It sends no request.
func (p *Provider) ListModels(context.Context) ([]ai.Model, error) {
	return []ai.Model{
		imageModel(ModelSoulV2, "SOUL V2"),
		imageModel(ModelSoulCinema, "SOUL Cinema"),
	}, nil
}

// imageModel returns a model that reads text and gives images.
func imageModel(id, name string) ai.Model {
	return ai.Model{
		ID:     id,
		Name:   name,
		Input:  []ai.Modality{ai.ModalityText},
		Output: []ai.Output{ai.OutputImage},
	}
}
