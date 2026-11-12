// Package catalog resolves a spec to a model bound to its provider. A
// spec is "<provider>/<model-id>", or a bare model ID that one provider
// serves.
//
// A caller must obey these rules:
//
//   - A catalog holds one provider per ID and capability. The last
//     registration wins.
//   - A spec names a registered model. To use a model that the SDK does
//     not list, register it with [Catalog.RegisterModel], or list it
//     with [Catalog.FetchModels].
//   - The catalog resolves only. A caller makes the call on the bound
//     model, or passes it to agent.New.
//   - A driver starts an agent for a spec. A provider with a driver
//     uses it. A text provider with no driver uses the default driver.
//   - The providers and the drivers have separate models. A method of a
//     model reads only the models of the providers. [Catalog.Driver]
//     reads the models of a driver.
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

// ErrUnknownModel is the error of a spec that names no registered
// model. A caller tests for it with [errors.Is], and reads the details
// from an [UnknownModelError]. To add a model, call
// [Catalog.RegisterModel], [Catalog.FetchModels], or
// [Catalog.RegisterDriver].
var ErrUnknownModel = errors.New("catalog: unknown model")

// ErrAmbiguousModel is the error of a bare model ID that two or more
// providers registered. A caller tests for it with [errors.Is], and
// reads the specs from an [AmbiguousModelError].
var ErrAmbiguousModel = errors.New("catalog: ambiguous model")

// UnknownModelError is the error of a spec that names no registered
// model. It wraps [ErrUnknownModel]. A caller reads it with [errors.As]
// to tell a person how to correct the spec.
type UnknownModelError struct {
	// Spec is the spec that the caller gave.
	Spec string
	// Provider is the ID of the provider or the driver of Spec, if the
	// catalog has it. It is empty for a bare model ID, and for a spec
	// whose provider the catalog does not have.
	Provider string
	// Driver reports that Provider is a driver.
	Driver bool
	// Providers lists the ID of each provider and driver of the
	// catalog, sorted, if Spec names a provider that the catalog does
	// not have. It is empty otherwise.
	Providers []string
}

func (e *UnknownModelError) Error() string {
	msg := fmt.Sprintf("%v %q", ErrUnknownModel, e.Spec)
	id, modelID, _ := strings.Cut(e.Spec, "/")
	switch {
	case e.Provider != "" && e.Driver:
		return msg + fmt.Sprintf(": driver %q has no model %q", e.Provider, modelID)
	case e.Provider != "":
		return msg + fmt.Sprintf(": provider %q has no model %q", e.Provider, modelID)
	case len(e.Providers) > 0:
		return msg + fmt.Sprintf(": no provider %q: use one of %s", id, strings.Join(e.Providers, ", "))
	default:
		return msg
	}
}

// Unwrap returns [ErrUnknownModel].
func (e *UnknownModelError) Unwrap() error { return ErrUnknownModel }

// ErrNoDriver is the error of a spec that no driver can start. Its
// provider has no driver of its own, and either it has no text
// generation, or the catalog has no [Catalog.DefaultDriver].
var ErrNoDriver = errors.New("no driver")

// ErrDriverOnly is the error of a method of a model on a spec whose
// provider has only a driver. Start the agent of the spec with
// [Catalog.Driver].
var ErrDriverOnly = errors.New("only a driver")

// AmbiguousModelError is the error of a bare model ID that two or more
// providers registered. It wraps [ErrAmbiguousModel]. A caller reads
// Specs to offer the full specs.
type AmbiguousModelError struct {
	// ID is the bare model ID that the caller gave.
	ID string
	// Specs are the full specs that match ID, sorted.
	Specs []string
}

func (e *AmbiguousModelError) Error() string {
	return fmt.Sprintf("%v %q: use one of %s", ErrAmbiguousModel, e.ID, strings.Join(e.Specs, ", "))
}

// Unwrap returns [ErrAmbiguousModel].
func (e *AmbiguousModelError) Unwrap() error { return ErrAmbiguousModel }

// Catalog is a registry of providers, drivers, and their models. Each
// provider ID has one entry with a provider for each capability, and the
// providers share one model index. The drivers of agents have their own
// model index, because a driver cannot serve a call to a model. Create a
// Catalog with [New]. A Catalog is safe for concurrent use.
type Catalog struct {
	mu            sync.RWMutex
	providers     map[string]*providers
	drivers       map[string]agent.Driver
	defaultDriver agent.Driver

	// models holds each model of a provider by its full spec.
	models map[string]ai.Model
	// driverModels holds each model of a driver by its full spec.
	driverModels map[string]ai.Model
}

// providers holds the providers registered under one ID.
type providers struct {
	text     ai.TextProvider
	image    ai.ImageProvider
	decision ai.DecisionProvider
}

// New returns an empty catalog.
func New() *Catalog {
	return &Catalog{
		providers:    make(map[string]*providers),
		drivers:      make(map[string]agent.Driver),
		models:       make(map[string]ai.Model),
		driverModels: make(map[string]ai.Model),
	}
}

// entryLocked returns the providers of id, and adds an empty entry if
// id has none.
func (c *Catalog) entryLocked(id string) *providers {
	e, ok := c.providers[id]
	if !ok {
		e = &providers{}
		c.providers[id] = e
	}
	return e
}

// providersOf returns a copy of the providers of id. It is empty if id
// has none.
func (c *Catalog) providersOf(id string) providers {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.providers[id]
	if !ok {
		return providers{}
	}
	return *e
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
	c.entryLocked(id).text = p
	addModels(c.models, id, models)
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
	c.entryLocked(id).image = p
	addModels(c.models, id, models)
}

// RegisterDecisionProvider registers p for decisions under id, and
// adds models to the model index.
func (c *Catalog) RegisterDecisionProvider(
	id string,
	p ai.DecisionProvider,
	models ...ai.Model,
) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entryLocked(id).decision = p
	addModels(c.models, id, models)
}

// RegisterDriver registers d as the driver of agents under id, and adds
// models to the models of the drivers. A spec of the provider id then
// starts an agent with d, such as "claude-code/claude-opus-5-5". The
// methods of a model, such as [Catalog.Model], do not read these
// models.
func (c *Catalog) RegisterDriver(
	id string,
	d agent.Driver,
	models ...ai.Model,
) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.drivers[id] = d
	addModels(c.driverModels, id, models)
}

// DefaultDriver sets the driver of the text providers that have no
// driver of their own, such as a harness. A spec of such a provider then
// starts an agent of the default driver over the model of the spec.
func (c *Catalog) DefaultDriver(d agent.Driver) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.defaultDriver = d
}

// RegisterModel adds m to the models of the providers under the
// provider id.
func (c *Catalog) RegisterModel(id string, m ai.Model) {
	c.mu.Lock()
	defer c.mu.Unlock()
	addModels(c.models, id, []ai.Model{m})
}

func addModels(index map[string]ai.Model, id string, models []ai.Model) {
	for _, m := range models {
		index[id+"/"+m.ID] = m
	}
}

// Specs returns the spec of each model of a provider, sorted. A
// provider that registered no models adds no specs. [Catalog.DriverSpecs]
// returns the specs of the drivers.
func (c *Catalog) Specs() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return slices.Sorted(maps.Keys(c.models))
}

// DriverSpecs returns the spec of each model of a driver, sorted.
func (c *Catalog) DriverSpecs() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return slices.Sorted(maps.Keys(c.driverModels))
}

// Model returns the model of spec, without a provider. It reads only
// the models of the providers.
func (c *Catalog) Model(spec string) (ai.Model, error) {
	m, _, err := c.resolve(spec)
	return m, err
}

// LanguageModel resolves spec to a model bound to its text provider. If
// the provider of spec has no text generation, or the [ai.Model.Output]
// of the model has no text, the error wraps [errors.ErrUnsupported].
func (c *Catalog) LanguageModel(spec string) (ai.LanguageModel, error) {
	m, id, err := c.resolve(spec)
	if err != nil {
		return ai.LanguageModel{}, err
	}
	p := c.providersOf(id).text
	if p == nil {
		return ai.LanguageModel{}, unsupported(id, "text generation")
	}
	if err := checkOutput(id, m, ai.OutputText); err != nil {
		return ai.LanguageModel{}, err
	}
	return ai.LanguageModel{Model: m, Provider: p}, nil
}

// ImageModel resolves spec to a model bound to its image provider. If
// the provider of spec has no image generation, or the
// [ai.Model.Output] of the model has no image, the error wraps
// [errors.ErrUnsupported].
func (c *Catalog) ImageModel(spec string) (ai.ImageModel, error) {
	m, id, err := c.resolve(spec)
	if err != nil {
		return ai.ImageModel{}, err
	}
	p := c.providersOf(id).image
	if p == nil {
		return ai.ImageModel{}, unsupported(id, "image generation")
	}
	if err := checkOutput(id, m, ai.OutputImage); err != nil {
		return ai.ImageModel{}, err
	}
	return ai.ImageModel{Model: m, Provider: p}, nil
}

// DecisionModel resolves spec to a model bound to its decision
// provider. If the provider of spec has no decisions, or the
// [ai.Model.Output] of the model has no decisions, the error wraps
// [errors.ErrUnsupported].
func (c *Catalog) DecisionModel(spec string) (ai.DecisionModel, error) {
	m, id, err := c.resolve(spec)
	if err != nil {
		return ai.DecisionModel{}, err
	}
	p := c.providersOf(id).decision
	if p == nil {
		return ai.DecisionModel{}, unsupported(id, "decisions")
	}
	if err := checkOutput(id, m, ai.OutputDecisions); err != nil {
		return ai.DecisionModel{}, err
	}
	return ai.DecisionModel{Model: m, Provider: p}, nil
}

// Driver resolves spec to the driver that starts its agent, and to its
// model. The model must be registered. A provider with a driver uses
// it, with the model of the driver.
// A text provider with no driver uses the [Catalog.DefaultDriver], with
// the model of the provider.
//
// A bare model ID resolves as in [Catalog.LanguageModel] first. So if a
// text provider and another provider with a driver both serve the ID,
// the text provider wins, and the ID names the same model in both
// methods. A bare ID resolves to the models of the drivers only when no
// provider registered it. To start the agent of a driver, give its full
// spec, such as "claude-code/claude-opus-5-5".
//
// If no driver can start the spec, the error wraps [ErrNoDriver].
func (c *Catalog) Driver(spec string) (agent.Driver, ai.Model, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	prefix, modelID, hasProvider := strings.Cut(spec, "/")
	if d, ok := c.drivers[prefix]; ok && hasProvider {
		m, err := c.driverModelLocked(prefix, modelID)
		return d, m, err
	}

	m, id, err := resolveIn(c.models, spec, c.hasModelProviderLocked)
	providerSpec := hasProvider && c.hasModelProviderLocked(prefix)
	if errors.Is(err, ErrUnknownModel) && !providerSpec {
		m, id, err = resolveBare(c.driverModels, spec)
	}
	if err != nil {
		return nil, ai.Model{}, c.withProvidersLocked(err)
	}
	if d, ok := c.drivers[id]; ok {
		m, err := c.driverModelLocked(id, m.ID)
		return d, m, err
	}
	e, ok := c.providers[id]
	if !ok || e.text == nil {
		return nil, ai.Model{}, fmt.Errorf(
			"catalog: provider %q has %w and no text generation",
			id,
			ErrNoDriver,
		)
	}
	if c.defaultDriver == nil {
		return nil, ai.Model{}, fmt.Errorf(
			"catalog: provider %q has %w: set the default driver with DefaultDriver",
			id,
			ErrNoDriver,
		)
	}
	return c.defaultDriver, m, nil
}

func (c *Catalog) driverModelLocked(id, modelID string) (ai.Model, error) {
	m, ok := c.driverModels[id+"/"+modelID]
	if !ok {
		return ai.Model{}, &UnknownModelError{
			Spec:     id + "/" + modelID,
			Provider: id,
			Driver:   true,
		}
	}
	return m, nil
}

// checkOutput returns an error that wraps [errors.ErrUnsupported] if the
// output of m is known and does not have want. A model with no output
// list passes, because most model lists do not say what a model gives.
func checkOutput(id string, m ai.Model, want ai.Output) error {
	if len(m.Output) == 0 || slices.Contains(m.Output, want) {
		return nil
	}
	return fmt.Errorf(
		"catalog: model %q gives no %s: %w",
		id+"/"+m.ID,
		want,
		errors.ErrUnsupported,
	)
}

func unsupported(id, capability string) error {
	return fmt.Errorf(
		"catalog: provider %q has no %s: %w",
		id,
		capability,
		errors.ErrUnsupported,
	)
}

// resolve returns the model of spec in the models of the providers, and
// the ID of its provider. A spec of a provider with only a driver fails
// with [ErrDriverOnly].
func (c *Catalog) resolve(spec string) (ai.Model, string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	id, _, hasProvider := strings.Cut(spec, "/")
	_, driver := c.drivers[id]
	driverOnly := hasProvider && driver && !c.hasModelProviderLocked(id)
	if driverOnly {
		return ai.Model{}, "", fmt.Errorf(
			"catalog: provider %q has %w: start its agent with Driver",
			id,
			ErrDriverOnly,
		)
	}
	m, id, err := resolveIn(c.models, spec, c.hasModelProviderLocked)
	return m, id, c.withProvidersLocked(err)
}

// withProvidersLocked adds the ID of each provider and driver to err,
// if err is an [UnknownModelError] of a spec whose provider the catalog
// does not have.
func (c *Catalog) withProvidersLocked(err error) error {
	var unknown *UnknownModelError
	if !errors.As(err, &unknown) {
		return err
	}
	unknownProvider := unknown.Provider == "" && strings.Contains(unknown.Spec, "/")
	if !unknownProvider {
		return err
	}
	ids := c.providerIDsLocked()
	for id := range c.drivers {
		if !c.hasModelProviderLocked(id) {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	unknown.Providers = ids
	return unknown
}

func (c *Catalog) hasModelProviderLocked(id string) bool {
	_, ok := c.providers[id]
	return ok
}

// resolveIn returns the model of spec in index and the ID of its
// provider. It tries a registered spec first, and then a bare model ID.
// If known reports the provider of spec, resolveIn does not try the
// bare ID, so the error names the provider.
func resolveIn(
	index map[string]ai.Model,
	spec string,
	known func(id string) bool,
) (ai.Model, string, error) {
	if m, ok := index[spec]; ok {
		id, _, _ := strings.Cut(spec, "/")
		return m, id, nil
	}

	id, _, hasProvider := strings.Cut(spec, "/")
	if hasProvider && known(id) {
		return ai.Model{}, "", &UnknownModelError{
			Spec:     spec,
			Provider: id,
		}
	}

	return resolveBare(index, spec)
}

// resolveBare resolves a model ID with no provider in index. It
// succeeds when exactly one provider registered the ID.
func resolveBare(index map[string]ai.Model, modelID string) (ai.Model, string, error) {
	var matches []string
	for spec := range index {
		_, id, _ := strings.Cut(spec, "/")
		if id == modelID {
			matches = append(matches, spec)
		}
	}

	switch len(matches) {
	case 0:
		return ai.Model{}, "", &UnknownModelError{Spec: modelID}
	case 1:
		id, _, _ := strings.Cut(matches[0], "/")
		return index[matches[0]], id, nil
	default:
		slices.Sort(matches)
		return ai.Model{}, "", &AmbiguousModelError{
			ID:    modelID,
			Specs: matches,
		}
	}
}
