package catalog

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/sonnes/gopi/pkg/ai"
)

// ErrNoModelList is the error of a fetch result for an ID that no
// provider or driver of the catalog lists models for. A caller that
// names a typo gets it, so the typo does not pass in silence.
var ErrNoModelList = errors.New("no model list")

// FetchResult is the result of one provider in [Catalog.FetchModels],
// or of one driver in [Catalog.FetchDriverModels].
type FetchResult struct {
	// Provider is the ID of the provider or of the driver.
	Provider string
	// Models is the number of models that the provider listed.
	Models int
	Err    error
}

// FetchModels asks each provider that implements [ai.ModelLister] for
// its models, and adds them to the models of the providers. If ids is
// not empty, it asks only the providers that ids names. It asks once
// for each provider ID, in ID order. A failed list does not stop the
// others. Its result carries the error, and the index keeps the models
// it had. An ID of ids with no provider that lists models gets a result
// with [ErrNoModelList].
//
// A listed model replaces a registered one field by field. See
// [MergeModel].
func (c *Catalog) FetchModels(ctx context.Context, ids ...string) []FetchResult {
	var results []FetchResult
	var listed []string
	for _, id := range c.providerIDs() {
		if !named(ids, id) {
			continue
		}
		l := c.lister(id)
		if l == nil {
			continue
		}
		listed = append(listed, id)
		models, err := l.ListModels(ctx)
		if err != nil {
			results = append(results, FetchResult{
				Provider: id,
				Err:      err,
			})
			continue
		}
		c.mergeModels(c.models, id, models, MergeModel)
		results = append(results, FetchResult{
			Provider: id,
			Models:   len(models),
		})
	}
	return withUnlisted(results, ids, listed)
}

// withUnlisted returns results with a result of [ErrNoModelList] for
// each ID of ids that listed does not have, in ID order.
func withUnlisted(results []FetchResult, ids, listed []string) []FetchResult {
	for _, id := range ids {
		done := slices.ContainsFunc(results, func(r FetchResult) bool {
			return r.Provider == id
		})
		if done || slices.Contains(listed, id) {
			continue
		}
		results = append(results, FetchResult{
			Provider: id,
			Err:      fmt.Errorf("catalog: %q has %w", id, ErrNoModelList),
		})
	}
	slices.SortStableFunc(results, func(a, b FetchResult) int {
		return cmp.Compare(a.Provider, b.Provider)
	})
	return results
}

// providerIDs returns the ID of each provider of any capability, sorted.
func (c *Catalog) providerIDs() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	ids := make(map[string]bool)
	for id := range c.textProviders {
		ids[id] = true
	}
	for id := range c.imageProviders {
		ids[id] = true
	}
	for id := range c.speechProviders {
		ids[id] = true
	}
	return slices.Sorted(maps.Keys(ids))
}

// lister returns the first provider of id that lists models: text, then
// image, then speech.
func (c *Catalog) lister(id string) ai.ModelLister {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, p := range []any{
		c.textProviders[id],
		c.imageProviders[id],
		c.speechProviders[id],
	} {
		if l, ok := p.(ai.ModelLister); ok {
			return l
		}
	}
	return nil
}

// FetchDriverModels asks each driver that implements [ai.ModelLister]
// for its models, and adds them to the models of the drivers. If ids is
// not empty, it asks only the drivers that ids names. It asks the
// drivers at the same time, because a driver starts its CLI to
// list. The results are in ID order. A failed list does not stop the
// others. Its result carries the error, and the index keeps the models
// it had. An ID of ids with no driver that lists models gets a result
// with [ErrNoModelList]. The thinking levels of the CLI replace the
// registered ones, as [MergeDriverModel] says. The index has no order,
// so the default of a driver stays the first model of its detector.
//
// It is a method of its own, and [Catalog.FetchModels] does not call
// it, because the list of a driver starts a process, and the CLI runs
// the hooks of its settings.
func (c *Catalog) FetchDriverModels(ctx context.Context, ids ...string) []FetchResult {
	names := ids
	ids, listers := c.driverListers(names)
	if len(ids) == 0 && len(names) == 0 {
		return nil
	}
	results := make([]FetchResult, len(ids))
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Go(func() {
			models, err := listers[i].ListModels(ctx)
			if err != nil {
				results[i] = FetchResult{
					Provider: id,
					Err:      err,
				}
				return
			}
			c.mergeModels(c.driverModels, id, models, MergeDriverModel)
			results[i] = FetchResult{
				Provider: id,
				Models:   len(models),
			}
		})
	}
	wg.Wait()
	return withUnlisted(results, names, ids)
}

// driverListers returns the ID of each driver that lists models and
// that names allows, sorted, and its lister at the same index.
func (c *Catalog) driverListers(names []string) ([]string, []ai.ModelLister) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var ids []string
	var listers []ai.ModelLister
	for _, id := range slices.Sorted(maps.Keys(c.drivers)) {
		l, ok := c.drivers[id].(ai.ModelLister)
		if !ok || !named(names, id) {
			continue
		}
		ids = append(ids, id)
		listers = append(listers, l)
	}
	return ids, listers
}

// named reports whether ids names id. An empty ids names every ID.
func named(ids []string, id string) bool {
	return len(ids) == 0 || slices.Contains(ids, id)
}

// mergeModels merges models into index under the ID id with merge.
func (c *Catalog) mergeModels(
	index map[string]ai.Model,
	id string,
	models []ai.Model,
	merge func(registered, listed ai.Model) ai.Model,
) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, m := range models {
		spec := id + "/" + m.ID
		old := index[spec]
		index[spec] = merge(old, m)
	}
}

// MergeModel returns listed with the registered values that the list
// does not have. [Catalog.FetchModels] merges with it, and so does the
// generator of models.json for a provider. A field that the list leaves
// zero keeps the registered value, because an API can return an ID with
// no name or no output cap. The prices and the thinking levels of a
// registered model stay if it has them: the price table of a provider package is the price of its
// own API, and the Anthropic API cannot say if a model can turn
// thinking off. A model with no registered prices keeps the listed
// prices, such as the prices of OpenRouter.
func MergeModel(registered, listed ai.Model) ai.Model {
	pricing := registered.Pricing
	if !hasPrices(pricing) {
		pricing = listed.Pricing
	}
	return ai.Model{
		ID:             listed.ID,
		Name:           cmp.Or(listed.Name, registered.Name),
		MaxTokens:      cmp.Or(listed.MaxTokens, registered.MaxTokens),
		ContextWindow:  cmp.Or(listed.ContextWindow, registered.ContextWindow),
		Input:          firstSet(listed.Input, registered.Input),
		Reasoning:      listed.Reasoning || registered.Reasoning,
		ThinkingLevels: firstSet(registered.ThinkingLevels, listed.ThinkingLevels),
		Pricing:        pricing,
	}
}

// MergeDriverModel is [MergeModel] for the model of a driver, with one
// change: the listed thinking levels replace the registered ones. The
// CLI knows the levels that it takes, and a new version of the CLI can
// add one. [Catalog.FetchDriverModels] merges with it, and so does the
// generator of models.json.
func MergeDriverModel(registered, listed ai.Model) ai.Model {
	m := MergeModel(registered, listed)
	m.ThinkingLevels = firstSet(listed.ThinkingLevels, registered.ThinkingLevels)
	return m
}

// hasPrices reports whether p has any price.
func hasPrices(p ai.Pricing) bool {
	base := p.Input + p.Output + p.CacheRead + p.CacheWrite + p.CacheWriteLong
	return base != 0 || len(p.Tiers) > 0
}

// firstSet returns a if it has elements, and b if not. It is [cmp.Or]
// for slices, which are not comparable.
func firstSet[T any](a, b []T) []T {
	if len(a) > 0 {
		return a
	}
	return b
}
