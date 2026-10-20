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
//   - A driver starts an agent for a spec. A provider with a driver
//     uses it. A text provider with no driver uses the default driver.
//   - A bare model ID does not count a provider that has only a driver,
//     unless no other provider registered the ID.
//
// The reasons are in docs/concepts/catalog/catalog.md.
package catalog

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
)

// ErrUnknownModel is the error of a spec that no provider serves. A
// caller tests for it with [errors.Is].
var ErrUnknownModel = errors.New("catalog: unknown model")

// Catalog is a registry of providers and their models. Each capability
// has its own map of providers, and all of them share one model index.
// The drivers of agents are a capability too. Create one with [New]. A
// Catalog is safe for concurrent use.
type Catalog struct {
	mu              sync.RWMutex
	textProviders   map[string]ai.TextProvider
	imageProviders  map[string]ai.ImageProvider
	speechProviders map[string]ai.SpeechProvider
	drivers         map[string]agent.Driver
	defaultDriver   agent.Driver

	// models holds each registered model by its full spec.
	models map[string]ai.Model
}

// New returns an empty catalog.
func New() *Catalog {
	return &Catalog{
		textProviders:   make(map[string]ai.TextProvider),
		imageProviders:  make(map[string]ai.ImageProvider),
		speechProviders: make(map[string]ai.SpeechProvider),
		drivers:         make(map[string]agent.Driver),
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

// RegisterDriver registers d as the driver of agents under id, and adds
// models to the model index. A spec of the provider id then starts an
// agent with d, such as "claude-code/claude-opus-5-5". If id has only a
// driver, a bare model ID skips its models when another provider
// registered the same ID. [Catalog.Driver] says why.
func (c *Catalog) RegisterDriver(
	id string,
	d agent.Driver,
	models ...ai.Model,
) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.drivers[id] = d
	c.addModelsLocked(id, models)
}

// DefaultDriver sets the driver of the text providers that have no
// driver of their own, such as a harness. A spec of such a provider then
// starts an agent of the default driver over the model of the spec.
func (c *Catalog) DefaultDriver(d agent.Driver) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.defaultDriver = d
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

// Model returns the model of spec, without a provider. A bare model ID
// does not count a provider that has only a driver.
func (c *Catalog) Model(spec string) (ai.Model, error) {
	m, _, err := c.resolve(spec, false)
	return m, err
}

// LanguageModel resolves spec to a model bound to its text provider. A
// bare model ID does not count a provider that has only a driver,
// because a driver cannot serve a call to a model.
func (c *Catalog) LanguageModel(spec string) (ai.LanguageModel, error) {
	m, id, err := c.resolve(spec, false)
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
	m, id, err := c.resolve(spec, false)
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
	m, id, err := c.resolve(spec, false)
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

// Driver resolves spec to the driver that starts its agent, and to its
// model. A provider with a driver uses it. A text provider with no
// driver uses the [Catalog.DefaultDriver].
//
// A bare model ID resolves as in [Catalog.LanguageModel] first. So if a
// text provider and a provider with only a driver both serve the ID, the
// text provider wins, and the ID names the same model in both methods.
// A provider with only a driver serves a bare ID when no other provider
// registered it. To start the agent of a driver, give its full spec,
// such as "claude-code/claude-opus-5-5".
func (c *Catalog) Driver(spec string) (agent.Driver, ai.Model, error) {
	m, id, err := c.resolve(spec, true)
	if err != nil {
		return nil, ai.Model{}, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if d, ok := c.drivers[id]; ok {
		return d, m, nil
	}
	if _, ok := c.textProviders[id]; !ok {
		return nil, ai.Model{}, fmt.Errorf(
			"catalog: provider %q has no driver and no text generation",
			id,
		)
	}
	if c.defaultDriver == nil {
		return nil, ai.Model{}, fmt.Errorf(
			"catalog: provider %q needs the default driver: set one with DefaultDriver",
			id,
		)
	}
	return c.defaultDriver, m, nil
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
// registered provider, and a bare model ID. If drivers is true, a bare
// model ID that no other provider registered can resolve to a provider
// that has only a driver.
//
// The second form passes the ID through, because a provider serves new
// models before the SDK lists them.
func (c *Catalog) resolve(spec string, drivers bool) (ai.Model, string, error) {
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

	return c.resolveBareLocked(spec, drivers)
}

// hasProviderLocked reports whether id has a provider for any
// capability.
func (c *Catalog) hasProviderLocked(id string) bool {
	_, driver := c.drivers[id]
	return driver || c.hasModelProviderLocked(id)
}

// hasModelProviderLocked reports whether id has a text, image, or speech
// provider.
func (c *Catalog) hasModelProviderLocked(id string) bool {
	_, text := c.textProviders[id]
	_, image := c.imageProviders[id]
	_, speech := c.speechProviders[id]
	return text || image || speech
}

// driverOnlyLocked reports whether id has a driver and no text, image,
// or speech provider.
func (c *Catalog) driverOnlyLocked(id string) bool {
	_, driver := c.drivers[id]
	return driver && !c.hasModelProviderLocked(id)
}

// resolveBareLocked resolves a model ID with no provider. It succeeds
// when exactly one provider registered the ID. It does not count a
// provider that has only a driver. If drivers is true and no other
// provider registered the ID, it counts the providers with only a
// driver.
func (c *Catalog) resolveBareLocked(modelID string, drivers bool) (ai.Model, string, error) {
	var matches, driverMatches []string
	for spec := range c.models {
		provider, id, _ := strings.Cut(spec, "/")
		if id != modelID {
			continue
		}
		if c.driverOnlyLocked(provider) {
			driverMatches = append(driverMatches, spec)
		} else {
			matches = append(matches, spec)
		}
	}
	if len(matches) == 0 && drivers {
		matches = driverMatches
	}

	switch len(matches) {
	case 0:
		return ai.Model{}, "", fmt.Errorf("%w %q", ErrUnknownModel, modelID)
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
