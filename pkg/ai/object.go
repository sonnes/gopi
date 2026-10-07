package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
)

// ObjectProvider generates a JSON object that matches a schema. A
// provider implements it together with [TextProvider].
type ObjectProvider interface {
	GenerateObject(
		ctx context.Context,
		model Model,
		messages []Message,
		schema *jsonschema.Schema,
		opts Options,
	) (*ObjectResponse, error)
}

// ObjectResponse is the raw response of an [ObjectProvider]. Raw is the
// JSON text of the object.
type ObjectResponse struct {
	Raw   string
	Usage Usage
}

// ObjectResult is the typed result of [GenerateObject].
type ObjectResult[T any] struct {
	Object T
	Raw    string
	Usage  Usage
}

// GenerateObject generates a T from lm. It derives the schema from T. If
// the provider of lm does not implement [ObjectProvider], GenerateObject
// returns an error.
func GenerateObject[T any](
	ctx context.Context,
	lm LanguageModel,
	messages []Message,
	opts ...Options,
) (*ObjectResult[T], error) {
	provider, ok := lm.Provider.(ObjectProvider)
	if !ok {
		return nil, errors.New("ai: provider does not support object generation")
	}

	schema, err := jsonschema.For[T](nil)
	if err != nil {
		return nil, fmt.Errorf("ai: schema for %T: %w", *new(T), err)
	}

	resp, err := provider.GenerateObject(
		ctx,
		lm.Model,
		messages,
		schema,
		JoinOptions(opts...),
	)
	if err != nil {
		return nil, err
	}

	var obj T
	if err := json.Unmarshal([]byte(resp.Raw), &obj); err != nil {
		return nil, fmt.Errorf("ai: decode object: %w", err)
	}

	return &ObjectResult[T]{
		Object: obj,
		Raw:    resp.Raw,
		Usage:  resp.Usage,
	}, nil
}
