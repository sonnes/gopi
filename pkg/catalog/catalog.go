// Package catalog resolves a spec to a model bound to its provider. A
// spec is "<provider>/<model-id>", or a bare model ID that one provider
// serves.
//
// A caller must obey these rules:
//
//   - A catalog holds one provider per ID and capability. The last
//     registration wins.
//   - An ID that the provider did not register passes through as a
//     model with that ID and no metadata, such as MaxTokens.
//   - The catalog resolves only. A caller makes the call on the bound
//     model, or passes it to agent.New.
//
// The reasons are in docs/concepts/catalog/catalog.md.
package catalog

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/sonnes/gopi/pkg/ai"
)

// Catalog is a registry of providers and their models. Each capability
// has its own map of providers, and all of them share one model index.
// Create one with [New]. A Catalog is safe for concurrent use.
type Catalog struct {
	mu              sync.RWMutex
	textProviders   map[string]ai.TextProvider
	imageProviders  map[string]ai.ImageProvider
	speechProviders map[string]ai.SpeechProvider

	// models holds each registered model by its full spec.
	models map[string]ai.Model
}

// New returns an empty catalog.
func New() *Catalog {
	return &Catalog{
		textProviders:   make(map[string]ai.TextProvider),
		imageProviders:  make(map[string]ai.ImageProvider),
		speechProviders: make(map[string]ai.SpeechProvider),
		models:          make(map[string]ai.Model),
	}
}

// RegisterTextProvider registers p for text generation under id, and
// adds models to the model index.
func (c *Catalog) RegisterTextProvider(
	id string,
	p ai.TextProvider,
	models ...ai.Model,
) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.textProviders[id] = p
	c.addModelsLocked(id, models)
}

// RegisterImageProvider registers p for image generation under id, and
// adds models to the model index.
func (c *Catalog) RegisterImageProvider(
	id string,
	p ai.ImageProvider,
	models ...ai.Model,
) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.imageProviders[id] = p
	c.addModelsLocked(id, models)
}

// RegisterSpeechProvider registers p for speech generation under id, and
// adds models to the model index.
func (c *Catalog) RegisterSpeechProvider(
	id string,
	p ai.SpeechProvider,
	models ...ai.Model,
) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.speechProviders[id] = p
	c.addModelsLocked(id, models)
}

// RegisterModel adds m to the model index under the provider id.
func (c *Catalog) RegisterModel(id string, m ai.Model) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.addModelsLocked(id, []ai.Model{m})
}

func (c *Catalog) addModelsLocked(id string, models []ai.Model) {
	for _, m := range models {
		c.models[id+"/"+m.ID] = m
	}
}

// Specs returns the spec of every registered model, sorted. A provider
// that registered no models adds no specs.
func (c *Catalog) Specs() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return slices.Sorted(maps.Keys(c.models))
}

// Model returns the model of spec, without a provider.
func (c *Catalog) Model(spec string) (ai.Model, error) {
	m, _, err := c.resolve(spec)
	return m, err
}

// LanguageModel resolves spec to a model bound to its text provider.
func (c *Catalog) LanguageModel(spec string) (ai.LanguageModel, error) {
	m, id, err := c.resolve(spec)
	if err != nil {
		return ai.LanguageModel{}, err
	}
	c.mu.RLock()
	p, ok := c.textProviders[id]
	c.mu.RUnlock()
	if !ok {
		return ai.LanguageModel{}, unsupported(id, "text")
	}
	return ai.LanguageModel{Model: m, Provider: p}, nil
}

// ImageModel resolves spec to a model bound to its image provider.
func (c *Catalog) ImageModel(spec string) (ai.ImageModel, error) {
	m, id, err := c.resolve(spec)
	if err != nil {
		return ai.ImageModel{}, err
	}
	c.mu.RLock()
	p, ok := c.imageProviders[id]
	c.mu.RUnlock()
	if !ok {
		return ai.ImageModel{}, unsupported(id, "image")
	}
	return ai.ImageModel{Model: m, Provider: p}, nil
}

// SpeechModel resolves spec to a model bound to its speech provider.
func (c *Catalog) SpeechModel(spec string) (ai.SpeechModel, error) {
	m, id, err := c.resolve(spec)
	if err != nil {
		return ai.SpeechModel{}, err
	}
	c.mu.RLock()
	p, ok := c.speechProviders[id]
	c.mu.RUnlock()
	if !ok {
		return ai.SpeechModel{}, unsupported(id, "speech")
	}
	return ai.SpeechModel{Model: m, Provider: p}, nil
}

func unsupported(id, capability string) error {
	return fmt.Errorf(
		"catalog: provider %q does not support %s generation",
		id,
		capability,
	)
}

// resolve returns the model of spec and the ID of its provider. It tries
// three forms in order: a registered spec, an unregistered ID on a
// registered provider, and a bare model ID.
//
// The second form passes the ID through, because a provider serves new
// models before the SDK lists them.
func (c *Catalog) resolve(spec string) (ai.Model, string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if m, ok := c.models[spec]; ok {
		id, _, _ := strings.Cut(spec, "/")
		return m, id, nil
	}

	id, modelID, hasProvider := strings.Cut(spec, "/")
	if hasProvider && c.hasProviderLocked(id) {
		return ai.Model{ID: modelID, Name: modelID}, id, nil
	}

	return c.resolveBareLocked(spec)
}

// hasProviderLocked reports whether id has a provider for any
// capability.
func (c *Catalog) hasProviderLocked(id string) bool {
	_, text := c.textProviders[id]
	_, image := c.imageProviders[id]
	_, speech := c.speechProviders[id]
	return text || image || speech
}

// resolveBareLocked resolves a model ID with no provider. It succeeds
// when exactly one provider registered the ID.
func (c *Catalog) resolveBareLocked(modelID string) (ai.Model, string, error) {
	var matches []string
	for spec := range c.models {
		_, id, _ := strings.Cut(spec, "/")
		if id == modelID {
			matches = append(matches, spec)
		}
	}

	switch len(matches) {
	case 0:
		return ai.Model{}, "", fmt.Errorf("catalog: unknown model %q", modelID)
	case 1:
		id, _, _ := strings.Cut(matches[0], "/")
		return c.models[matches[0]], id, nil
	default:
		slices.Sort(matches)
		return ai.Model{}, "", fmt.Errorf(
			"catalog: ambiguous model %q: use one of %s",
			modelID,
			strings.Join(matches, ", "),
		)
	}
}
