package openrouter

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/sonnes/gopi/pkg/ai"
)

// tokensPerPriceUnit is the number of tokens that an [ai.Pricing] price
// is for. OpenRouter gives the price of one token.
const tokensPerPriceUnit = 1_000_000

// priceDigits is the number of decimal places that a price keeps, so
// that the conversion to a price for one million tokens has no float
// error.
const priceDigits = 6

// maxErrorBody is the most bytes of an error body that an error holds.
const maxErrorBody = 1024

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
		InputModalities []string `json:"input_modalities"`
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

// efforts maps a reasoning effort of OpenRouter to an
// [ai.ThinkingLevel]. "minimal" has no level.
var efforts = map[string]ai.ThinkingLevel{
	"none":   ai.ThinkingOff,
	"low":    ai.ThinkingLow,
	"medium": ai.ThinkingMedium,
	"high":   ai.ThinkingHigh,
	"xhigh":  ai.ThinkingXHigh,
	"max":    ai.ThinkingMax,
}

// levelOrder is the order of the thinking levels of a listed model.
var levelOrder = []ai.ThinkingLevel{
	ai.ThinkingOff,
	ai.ThinkingLow,
	ai.ThinkingMedium,
	ai.ThinkingHigh,
	ai.ThinkingXHigh,
	ai.ThinkingMax,
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

// ListModels lists the text models with tools of GET /models, with
// their limits, their input modalities, their thinking levels, and their
// prices. It skips the models of the Batch API. A price that OpenRouter
// gives as negative, such as the price of a router, is unknown and
// stays zero. The output cap is at most the context window, and a tier
// that starts after the context window is not in the list.
func (p *Provider) ListModels(ctx context.Context) ([]ai.Model, error) {
	body, err := p.get(ctx, "/models?"+toolModels)
	if err != nil {
		return nil, fmt.Errorf("openrouter: list models: %w", err)
	}
	var list listResponse
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("openrouter: list models: %w", err)
	}

	models := make([]ai.Model, 0, len(list.Data))
	for _, m := range list.Data {
		if strings.HasSuffix(m.ID, batchSuffix) {
			continue
		}
		models = append(models, m.model())
	}
	return models, nil
}

// get sends a GET request to path under the base URL, and returns the
// body of a response with status 200.
func (p *Provider) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.cfg.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	if p.cfg.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.cfg.apiKey)
	}
	client := cmp.Or(p.cfg.httpClient, http.DefaultClient)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return nil, fmt.Errorf("%s: %s", resp.Status, body)
	}
	return io.ReadAll(resp.Body)
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

// levels returns the thinking levels of efforts, in [levelOrder].
func levels(names []string) []ai.ThinkingLevel {
	var out []ai.ThinkingLevel
	for _, level := range levelOrder {
		known := slices.ContainsFunc(names, func(name string) bool {
			return efforts[name] == level
		})
		if known {
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
