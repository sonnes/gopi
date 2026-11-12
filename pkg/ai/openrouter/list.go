package openrouter

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/sonnes/gopi/internal/httpapi"
	"github.com/sonnes/gopi/pkg/ai"
)

// tokensPerPriceUnit is the number of tokens that an [ai.Pricing] price
// is for. OpenRouter gives the price of one token.
const tokensPerPriceUnit = 1_000_000

// priceDigits is the number of decimal places that a price keeps, so
// that the conversion to a price for one million tokens has no float
// error.
const priceDigits = 6

// listResponse is the body of GET /models.
type listResponse struct {
	Data []listedModel `json:"data"`
}

// listedModel holds the fields of a listed model that gopi reads.
type listedModel struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ContextLength *int   `json:"context_length"`
	Architecture  struct {
		InputModalities  []string `json:"input_modalities"`
		OutputModalities []string `json:"output_modalities"`
	} `json:"architecture"`
	Pricing     listedPricing `json:"pricing"`
	TopProvider struct {
		ContextLength       *int `json:"context_length"`
		MaxCompletionTokens *int `json:"max_completion_tokens"`
	} `json:"top_provider"`
	Reasoning *struct {
		SupportedEfforts []string `json:"supported_efforts"`
	} `json:"reasoning"`
}

// listedPricing holds the prices of a listed model, in US dollars for
// one token. An override with min_prompt_tokens is a price tier.
type listedPricing struct {
	Prompt           string `json:"prompt"`
	Completion       string `json:"completion"`
	InputCacheRead   string `json:"input_cache_read"`
	InputCacheWrite  string `json:"input_cache_write"`
	InputCacheWrite1 string `json:"input_cache_write_1h"`
	Overrides        []struct {
		MinPromptTokens  int      `json:"min_prompt_tokens"`
		UTCStart         *int     `json:"utc_start"`
		UTCEnd           *int     `json:"utc_end"`
		UTCDays          []string `json:"utc_days"`
		Prompt           string   `json:"prompt"`
		Completion       string   `json:"completion"`
		InputCacheRead   string   `json:"input_cache_read"`
		InputCacheWrite  string   `json:"input_cache_write"`
		InputCacheWrite1 string   `json:"input_cache_write_1h"`
	} `json:"overrides"`
}

// modalities maps an input modality of OpenRouter to an [ai.Modality].
// OpenRouter calls a PDF a file. The model reads no other modality
// that gopi sends.
var modalities = map[string]ai.Modality{
	"text":  ai.ModalityText,
	"image": ai.ModalityImage,
	"file":  ai.ModalityPDF,
}

// outputOrder is the order of the outputs of a listed model.
var outputOrder = []ai.Output{
	ai.OutputText,
	ai.OutputImage,
	ai.OutputDecisions,
}

// modalityOrder is the order of the input modalities of a listed model.
var modalityOrder = []ai.Modality{
	ai.ModalityText,
	ai.ModalityImage,
	ai.ModalityPDF,
}

// batchSuffix ends the ID of a model of the Batch API. An agent streams
// each call, so the list skips it.
const batchSuffix = ":batch"

// storageOnlyPrefix starts the ID of a model whose input_cache_write is
// only the storage of a write. OpenRouter bills a cache write of Google
// at the input price plus the storage of 5 minutes.
const storageOnlyPrefix = "google/"

// toolModels is the query of GET /models that keeps the models with
// tools. Each call of a gopi agent sends tools, so a model without them
// cannot serve an agent.
const toolModels = "supported_parameters=tools"

// decisionModels is the query of GET /models that keeps the decision
// models. A decision model takes no tools, and the default list of
// GET /models does not hold it.
const decisionModels = "output_modalities=decisions"

// ListModels lists the text models with tools of GET /models, and then
// the decision models, with their limits, their input modalities, their
// outputs, their thinking levels, and their prices. A model of both
// lists is in the result once. It skips the models of the Batch API.
//
// The list of the decision models is best effort. If it fails, the
// result holds the text models only, because a failed list of decision
// models must not stop a text session.
//
// A price that OpenRouter gives as negative, such as the price of a
// router, is unknown and stays zero. The output cap is at most the
// context window, and a tier that starts after the context window is
// not in the list.
func (p *Provider) ListModels(ctx context.Context) ([]ai.Model, error) {
	listed, err := p.listModels(ctx, toolModels)
	if err != nil {
		return nil, err
	}
	deciders, err := p.listModels(ctx, decisionModels)
	if err == nil {
		listed = append(listed, deciders...)
	}

	var models []ai.Model
	seen := make(map[string]bool)
	for _, m := range listed {
		if seen[m.ID] || strings.HasSuffix(m.ID, batchSuffix) {
			continue
		}
		seen[m.ID] = true
		models = append(models, m.model())
	}
	return models, nil
}

// listModels returns the models of GET /models with query.
func (p *Provider) listModels(ctx context.Context, query string) ([]listedModel, error) {
	url := p.cfg.baseURL + "/models?" + query
	body, err := httpapi.Get(ctx, p.cfg.httpClient, url, p.cfg.apiKey)
	if err != nil {
		return nil, fmt.Errorf("openrouter: list models: %w", err)
	}
	var list listResponse
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("openrouter: list models: %w", err)
	}
	return list.Data, nil
}

// model returns m as an [ai.Model].
func (m listedModel) model() ai.Model {
	window := cmp.Or(value(m.ContextLength), value(m.TopProvider.ContextLength))
	maxTokens := value(m.TopProvider.MaxCompletionTokens)
	if window > 0 {
		maxTokens = min(maxTokens, window)
	}
	pricing := m.Pricing.pricing()
	if window > 0 {
		pricing.Tiers = slices.DeleteFunc(pricing.Tiers, func(t ai.PriceTier) bool {
			return t.InputAbove >= window
		})
	}
	if strings.HasPrefix(m.ID, storageOnlyPrefix) {
		pricing = withWriteInput(pricing)
	}
	model := ai.Model{
		ID:            m.ID,
		Name:          m.Name,
		MaxTokens:     maxTokens,
		ContextWindow: window,
		Input:         input(m.Architecture.InputModalities),
		Output:        outputsOf(m.Architecture.OutputModalities),
		Reasoning:     m.Reasoning != nil,
		Pricing:       pricing,
	}
	if m.Reasoning != nil {
		model.ThinkingLevels = levels(m.Reasoning.SupportedEfforts)
	}
	return model
}

// withWriteInput returns p with the input price in each cache write
// price, for a model whose listed write price is only the storage.
func withWriteInput(p ai.Pricing) ai.Pricing {
	if p.CacheWrite > 0 {
		p.CacheWrite = round(p.Input + p.CacheWrite)
	}
	for i, t := range p.Tiers {
		if t.CacheWrite > 0 {
			p.Tiers[i].CacheWrite = round(t.Input + t.CacheWrite)
		}
	}
	return p
}

// input returns the modalities that gopi knows, in [modalityOrder].
func input(names []string) []ai.Modality {
	var out []ai.Modality
	for _, mod := range modalityOrder {
		known := slices.ContainsFunc(names, func(name string) bool {
			return modalities[name] == mod
		})
		if known {
			out = append(out, mod)
		}
	}
	return out
}

// outputsOf returns the outputs that gopi knows, in [outputOrder]. The
// value of an [ai.Output] is the name of its output modality on
// OpenRouter.
func outputsOf(names []string) []ai.Output {
	var out []ai.Output
	for _, o := range outputOrder {
		if slices.Contains(names, string(o)) {
			out = append(out, o)
		}
	}
	return out
}

// levels returns the thinking levels of the reasoning efforts of
// OpenRouter, in the order of [ai.ThinkingLevels]. An effort with no
// level, such as "minimal", is not in the list.
func levels(efforts []string) []ai.ThinkingLevel {
	var out []ai.ThinkingLevel
	for _, level := range ai.ThinkingLevels() {
		if slices.Contains(efforts, level.Effort()) {
			out = append(out, level)
		}
	}
	return out
}

// pricing returns the prices of p for one million tokens, with a tier
// for each override with min_prompt_tokens and no time of day. A tier
// applies from min_prompt_tokens, and [ai.PriceTier] applies above
// InputAbove, so InputAbove is one less. A price that the override does
// not give, or gives as unknown, is the base price.
func (p listedPricing) pricing() ai.Pricing {
	out := ai.Pricing{
		Input:          price(p.Prompt),
		Output:         price(p.Completion),
		CacheRead:      price(p.InputCacheRead),
		CacheWrite:     price(p.InputCacheWrite),
		CacheWriteLong: price(p.InputCacheWrite1),
	}
	for _, o := range p.Overrides {
		// A tier of ai.Pricing depends only on the size of the prompt,
		// so a price for a time of day has no place in it.
		byTime := o.UTCStart != nil || o.UTCEnd != nil || len(o.UTCDays) > 0
		if o.MinPromptTokens == 0 || byTime {
			continue
		}
		out.Tiers = append(out.Tiers, ai.PriceTier{
			InputAbove:     o.MinPromptTokens - 1,
			Input:          tierPrice(o.Prompt, p.Prompt),
			Output:         tierPrice(o.Completion, p.Completion),
			CacheRead:      tierPrice(o.InputCacheRead, p.InputCacheRead),
			CacheWrite:     tierPrice(o.InputCacheWrite, p.InputCacheWrite),
			CacheWriteLong: tierPrice(o.InputCacheWrite1, p.InputCacheWrite1),
		})
	}
	slices.SortFunc(out.Tiers, func(a, b ai.PriceTier) int {
		return a.InputAbove - b.InputAbove
	})
	return out
}

// price returns the price of one million tokens for the price s of one
// token. An empty, a negative, or a bad price is zero.
func price(s string) float64 {
	p, _ := knownPrice(s)
	return p
}

// tierPrice returns the price of a tier for its price s, or for the
// base price if s is empty, negative, or bad.
func tierPrice(s, base string) float64 {
	if p, ok := knownPrice(s); ok {
		return p
	}
	return price(base)
}

// knownPrice returns the price of one million tokens for the price s of
// one token, and false if s is empty, negative, or bad.
func knownPrice(s string) (float64, bool) {
	perToken, err := strconv.ParseFloat(s, 64)
	if err != nil || perToken < 0 {
		return 0, false
	}
	return round(perToken * tokensPerPriceUnit), true
}

// round returns p with [priceDigits] decimal places.
func round(p float64) float64 {
	scale := math.Pow10(priceDigits)
	return math.Round(p*scale) / scale
}

// value returns *n, or zero if n is nil.
func value(n *int) int {
	if n == nil {
		return 0
	}
	return *n
}
