package elevenlabs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/sonnes/gopi/pkg/ai"
)

type listedModel struct {
	ModelID            string `json:"model_id"`
	Name               string `json:"name"`
	CanDoTextToSpeech  bool   `json:"can_do_text_to_speech"`
	MaxCharactersLimit int    `json:"maximum_text_length_per_request"`
}

// ListModels lists the speech models of the models API. The API does
// not list the transcription models, such as scribe_v3.
func (p *Provider) ListModels(ctx context.Context) ([]ai.Model, error) {
	listed, err := p.models(ctx)
	if err != nil {
		return nil, err
	}
	var models []ai.Model
	for _, m := range listed {
		if !m.CanDoTextToSpeech {
			continue
		}
		models = append(models, ai.Model{
			ID:     m.ModelID,
			Name:   m.Name,
			Input:  []ai.Modality{ai.ModalityText},
			Output: []ai.Output{ai.OutputSpeech},
		})
	}
	return models, nil
}

// CheckKey checks the key with a call to the models API, which runs no
// model. The API gives no facts about a key, so the info is zero.
func (p *Provider) CheckKey(ctx context.Context) (ai.KeyInfo, error) {
	if _, err := p.models(ctx); err != nil {
		return ai.KeyInfo{}, err
	}
	return ai.KeyInfo{}, nil
}

// models reads the models API.
func (p *Provider) models(ctx context.Context) ([]listedModel, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.cfg.baseURL+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	raw, err := p.do(req, nil)
	if err != nil {
		return nil, fmt.Errorf("elevenlabs: models: %w", err)
	}
	var listed []listedModel
	if err := json.Unmarshal(raw, &listed); err != nil {
		return nil, fmt.Errorf("elevenlabs: models: %w", err)
	}
	return listed, nil
}
