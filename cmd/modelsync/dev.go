package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/sonnes/gopi/pkg/ai"
)

// devCatalog is the part of https://models.dev/api.json that the
// generator reads, by the provider ID of models.dev.
type devCatalog map[string]devProvider

// devProvider is one provider of models.dev.
type devProvider struct {
	Models map[string]devModel `json:"models"`
}

// devModel is one model of models.dev. Its prices are in US dollars for
// one million tokens, as in [ai.Pricing].
type devModel struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	ToolCall         bool   `json:"tool_call"`
	Reasoning        bool   `json:"reasoning"`
	ReasoningOptions []struct {
		Type   string   `json:"type"`
		Values []string `json:"values"`
	} `json:"reasoning_options"`
	Modalities struct {
		Input []string `json:"input"`
	} `json:"modalities"`
	Limit struct {
		Context int `json:"context"`
		Output  int `json:"output"`
	} `json:"limit"`
	Cost *devCost `json:"cost"`
}

// devCost is the price of a model. A tier of the type "context" applies
// to a prompt of more than Size tokens.
type devCost struct {
	devPrices
	Tiers []struct {
		devPrices
		Tier struct {
			Type string `json:"type"`
			Size int    `json:"size"`
		} `json:"tier"`
	} `json:"tiers"`
}

// devPrices are the prices of one tier.
type devPrices struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"`
}

// contextTier is the type of a tier by the size of the prompt.
const contextTier = "context"

// parseDev decodes the catalog of models.dev.
func parseDev(b []byte) (devCatalog, error) {
	var dev devCatalog
	if err := json.Unmarshal(b, &dev); err != nil {
		return nil, fmt.Errorf("models.dev: %w", err)
	}
	return dev, nil
}

// devInput maps an input modality of models.dev to an [ai.Modality].
var devInput = map[string]ai.Modality{
	"text":  ai.ModalityText,
	"image": ai.ModalityImage,
	"pdf":   ai.ModalityPDF,
}

// devEfforts maps a reasoning effort of models.dev to an
// [ai.ThinkingLevel].
var devEfforts = map[string]ai.ThinkingLevel{
	"none":   ai.ThinkingOff,
	"low":    ai.ThinkingLow,
	"medium": ai.ThinkingMedium,
	"high":   ai.ThinkingHigh,
	"xhigh":  ai.ThinkingXHigh,
	"max":    ai.ThinkingMax,
}

// levelOrder is the order of the thinking levels of a model.
var levelOrder = []ai.ThinkingLevel{
	ai.ThinkingOff,
	ai.ThinkingLow,
	ai.ThinkingMedium,
	ai.ThinkingHigh,
	ai.ThinkingXHigh,
	ai.ThinkingMax,
}

// model returns m as an [ai.Model]. A "toggle" reasoning option means
// that the model can turn thinking off.
func (m devModel) model() ai.Model {
	out := ai.Model{
		ID:            m.ID,
		Name:          m.Name,
		MaxTokens:     m.Limit.Output,
		ContextWindow: m.Limit.Context,
		Reasoning:     m.Reasoning,
	}
	for _, name := range m.Modalities.Input {
		if mod, ok := devInput[name]; ok {
			out.Input = append(out.Input, mod)
		}
	}
	levels := make(map[ai.ThinkingLevel]bool)
	for _, opt := range m.ReasoningOptions {
		if opt.Type == "toggle" {
			levels[ai.ThinkingOff] = true
		}
		for _, v := range opt.Values {
			if level, ok := devEfforts[v]; ok {
				levels[level] = true
			}
		}
	}
	for _, level := range levelOrder {
		if levels[level] {
			out.ThinkingLevels = append(out.ThinkingLevels, level)
		}
	}
	if m.Cost != nil {
		out.Pricing = m.Cost.pricing()
	}
	return out
}

// pricing returns c as an [ai.Pricing], with a tier for each tier of the
// type "context".
func (c devCost) pricing() ai.Pricing {
	p := ai.Pricing{
		Input:      c.Input,
		Output:     c.Output,
		CacheRead:  c.CacheRead,
		CacheWrite: c.CacheWrite,
	}
	for _, t := range c.Tiers {
		if t.Tier.Type != contextTier {
			continue
		}
		p.Tiers = append(p.Tiers, ai.PriceTier{
			InputAbove: t.Tier.Size,
			Input:      t.Input,
			Output:     t.Output,
			CacheRead:  t.CacheRead,
			CacheWrite: t.CacheWrite,
		})
	}
	slices.SortFunc(p.Tiers, func(a, b ai.PriceTier) int {
		return cmp.Compare(a.InputAbove, b.InputAbove)
	})
	return p
}
