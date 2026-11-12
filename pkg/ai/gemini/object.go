package gemini

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"google.golang.org/genai/interactions/models/interactions"

	"github.com/sonnes/gopi/internal/transcript"
	"github.com/sonnes/gopi/pkg/ai"
)

// GenerateObject asks for a JSON object that matches schema, with the
// schema as the response format. The text of the model output is the
// object. The request sends no tools, so the model answers with the
// object. An interaction that did not complete is an error, and so is
// an object that the output limit cut off, because it is not a valid
// object.
func (p *Provider) GenerateObject(
	ctx context.Context,
	model ai.Model,
	messages []ai.Message,
	schema *jsonschema.Schema,
	opts ai.Options,
) (*ai.ObjectResponse, error) {
	if p.err != nil {
		return nil, p.err
	}
	body, err := buildObjectRequest(model, messages, schema, opts)
	if err != nil {
		return nil, err
	}

	res, err := p.interact(ctx, body, opts)
	if err != nil {
		return nil, err
	}
	interaction := res.Interaction
	if interaction == nil {
		return nil, errors.New("gemini: the response has no interaction")
	}

	reason, err := stopReason(interaction.Status, false)
	if err != nil {
		return nil, err
	}
	if reason == ai.StopReasonLength {
		return nil, errors.New("gemini: the object is cut off at the output limit")
	}

	raw := outputText(interaction.Steps)
	if raw == "" {
		return nil, fmt.Errorf("gemini: no object in the interaction, status %q", interaction.Status)
	}
	return &ai.ObjectResponse{
		Raw:   raw,
		Usage: usageOf(interaction.Usage, model),
	}, nil
}

// buildObjectRequest does not convert the tools, because the request
// sends none.
func buildObjectRequest(
	model ai.Model,
	messages []ai.Message,
	schema *jsonschema.Schema,
	opts ai.Options,
) (interactions.CreateModelInteraction, error) {
	body, err := baseRequest(model, messages, opts)
	if err != nil {
		return body, err
	}

	schemaMap, err := transcript.Schema(schema)
	if err != nil {
		return body, fmt.Errorf("gemini: %w", err)
	}

	streaming := false
	mime := interactions.TextResponseFormatMimeTypeApplicationJSON
	format := interactions.NewResponseFormat(interactions.TextResponseFormat{
		MimeType: &mime,
		Schema:   schemaMap,
	})
	responseFormat := interactions.NewCreateModelInteractionResponseFormat(format)

	body.Stream = &streaming
	body.ResponseFormat = &responseFormat
	return body, nil
}

func outputText(steps []interactions.Step) string {
	var b strings.Builder
	for _, step := range steps {
		if step.ModelOutputStep == nil {
			continue
		}
		for _, text := range textParts(step.ModelOutputStep.Content) {
			b.WriteString(text)
		}
	}
	return b.String()
}
