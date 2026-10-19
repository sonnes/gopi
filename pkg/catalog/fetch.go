package catalog

import (
	"cmp"
	"context"
	"maps"
	"slices"

	"github.com/sonnes/gopi/pkg/ai"
)

// FetchResult is the result of one provider in [Catalog.FetchModels].
type FetchResult struct {
	Provider string
	// Models is the number of models that the provider listed.
	Models int
	Err    error
}

// FetchModels asks each provider that implements [ai.ModelLister] for
// its models, and adds them to the model index. It asks once for each
// provider ID, in ID order. A failed list does not stop the others. Its
// result carries the error, and the index keeps the models it had.
//
// A listed model replaces a registered one field by field. See
// [mergeModel].
func (c *Catalog) FetchModels(ctx context.Context) []FetchResult {
	var results []FetchResult
	for _, id := range c.providerIDs() {
		l := c.lister(id)
		if l == nil {
			continue
		}
		models, err := l.ListModels(ctx)
		if err != nil {
			results = append(results, FetchResult{
				Provider: id,
				Err:      err,
			})
			continue
		}
		c.mergeModels(id, models)
		results = append(results, FetchResult{
			Provider: id,
			Models:   len(models),
		})
	}
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

func (c *Catalog) mergeModels(id string, models []ai.Model) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, m := range models {
		spec := id + "/" + m.ID
		old := c.models[spec]
		c.models[spec] = mergeModel(old, m)
	}
}

// mergeModel returns listed with the registered values that the list
// does not have. A field that the list leaves zero keeps the registered
// value, because an API can return an ID with no name or no output cap.
// The prices and the thinking levels of a registered model always stay:
// no API lists prices, and the Anthropic API cannot say if a model can
// turn thinking off.
func mergeModel(registered, listed ai.Model) ai.Model {
	return ai.Model{
		ID:             listed.ID,
		Name:           cmp.Or(listed.Name, registered.Name),
		MaxTokens:      cmp.Or(listed.MaxTokens, registered.MaxTokens),
		ContextWindow:  cmp.Or(listed.ContextWindow, registered.ContextWindow),
		Input:          firstSet(listed.Input, registered.Input),
		Reasoning:      listed.Reasoning || registered.Reasoning,
		ThinkingLevels: firstSet(registered.ThinkingLevels, listed.ThinkingLevels),
		Pricing:        registered.Pricing,
	}
}

// firstSet returns a if it has elements, and b if not. It is [cmp.Or]
// for slices, which are not comparable.
func firstSet[T any](a, b []T) []T {
	if len(a) > 0 {
		return a
	}
	return b
}
