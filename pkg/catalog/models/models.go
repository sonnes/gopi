// Package models holds the default models of each provider and each
// driver of gopi, in the embedded file models.json. A provider package
// reads its typed models from it, and the detectors of package
// catalog/env register them.
//
// A caller must obey these rules:
//
//   - The file is keyed by the ID that the detectors of package
//     catalog/env use, such as "anthropic" or "claude-code".
//   - The file holds the defaults of a release. To get the models of a
//     provider now, call the FetchModels or FetchDriverModels method
//     of a catalog.
//   - Each function returns a copy, so a caller can change the result.
//
// The reasons are in docs/concepts/catalog/model-data.md.
package models

import (
	_ "embed"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/sonnes/gopi/internal/modelsfile"
	"github.com/sonnes/gopi/pkg/ai"
)

//go:embed models.json
var data []byte

// embedded returns the parsed embedded file. A broken file panics,
// because a test of this package checks it before a release.
var embedded = sync.OnceValue(func() modelsfile.File {
	f, err := modelsfile.Parse(data)
	if err != nil {
		panic(err)
	}
	return f
})

// Of returns the default models of the provider or driver id, with the
// default model first and the others in ID order. It returns nil for an
// ID that the file does not have.
func Of(id string) []ai.Model {
	e, ok := embedded()[id]
	if !ok {
		return nil
	}
	out := make([]ai.Model, 0, len(e.Models))
	for _, m := range e.Models {
		if m.ID == e.Default {
			out = append(out, clone(m.Model))
		}
	}
	for _, m := range e.Models {
		if m.ID != e.Default {
			out = append(out, clone(m.Model))
		}
	}
	return out
}

// Lookup returns the model modelID of the provider or driver id.
func Lookup(id, modelID string) (ai.Model, bool) {
	e, ok := embedded()[id]
	if !ok {
		return ai.Model{}, false
	}
	for _, m := range e.Models {
		if m.ID == modelID {
			return clone(m.Model), true
		}
	}
	return ai.Model{}, false
}

// Must is [Lookup] that panics if the file does not have the model. A
// provider package uses it for its typed models, so a model that leaves
// the file fails at the start of a test.
func Must(id, modelID string) ai.Model {
	m, ok := Lookup(id, modelID)
	if !ok {
		panic(fmt.Sprintf("models: no model %q of %q in models.json", modelID, id))
	}
	return m
}

// IDs returns each provider and driver ID of the file, sorted.
func IDs() []string {
	return slices.Sorted(maps.Keys(embedded()))
}

// clone returns m with its own copy of each slice.
func clone(m ai.Model) ai.Model {
	m.Input = slices.Clone(m.Input)
	m.ThinkingLevels = slices.Clone(m.ThinkingLevels)
	m.Pricing.Tiers = slices.Clone(m.Pricing.Tiers)
	return m
}
