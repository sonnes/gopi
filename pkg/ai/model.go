package ai

// Model names one model that a provider serves, and what is known about
// it. It holds no credentials and no provider identity, so the same
// value binds to any provider that serves it.
//
// A zero field is unknown, not absent. A model that a catalog passes
// through by ID has only its ID, and a provider then uses its defaults.
type Model struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// MaxTokens is the output cap of the model. If a call sets no
	// [MaxTokens] option, the provider uses this value.
	MaxTokens int `json:"max_tokens,omitempty"`
	// ContextWindow is the most tokens that one request can hold: the
	// prompt, the history, and the output.
	ContextWindow int `json:"context_window,omitempty"`
	// Input lists the kinds of content that the model reads.
	Input []Modality `json:"input,omitempty"`
	// Reasoning reports that the model reasons before it answers.
	Reasoning bool `json:"reasoning,omitempty"`
	// ThinkingLevels lists the [Thinking] levels that the model takes.
	// A provider does not check it. The API rejects a level that the
	// model does not take.
	ThinkingLevels []ThinkingLevel `json:"thinking_levels,omitempty"`
	// Pricing is the price of the tokens. A provider computes the cost
	// of each response from it, in [Usage.Cost].
	Pricing Pricing `json:"pricing,omitzero"`
}

// Modality is a kind of content that a model reads.
type Modality string

const (
	ModalityText  Modality = "text"
	ModalityImage Modality = "image"
	ModalityPDF   Modality = "pdf"
)

// tokensPerPriceUnit is the number of tokens that a price is for.
const tokensPerPriceUnit = 1_000_000

// Pricing is the price of each category of [Usage], in US dollars for
// one million tokens. CacheWrite is the price of the cache that a
// provider writes by default.
type Pricing struct {
	Input      float64 `json:"input,omitempty"`
	Output     float64 `json:"output,omitempty"`
	CacheRead  float64 `json:"cache_read,omitempty"`
	CacheWrite float64 `json:"cache_write,omitempty"`
	// Tiers holds the prices of a large request, in order of InputAbove.
	Tiers []PriceTier `json:"tiers,omitempty"`
}

// PriceTier is the price of a request whose input is more than
// InputAbove tokens. The input is the sum of Input, CacheRead, and
// CacheWrite. The tier prices the whole request, not only the tokens
// above the threshold, because that is how the providers bill.
type PriceTier struct {
	InputAbove int     `json:"input_above"`
	Input      float64 `json:"input,omitempty"`
	Output     float64 `json:"output,omitempty"`
	CacheRead  float64 `json:"cache_read,omitempty"`
	CacheWrite float64 `json:"cache_write,omitempty"`
}

// Cost returns the cost of u at these prices. It uses the last tier
// whose threshold the input passes, or the base prices if there is
// none. Zero prices give a zero cost.
func (p Pricing) Cost(u Usage) Cost {
	input := u.Input + u.CacheRead + u.CacheWrite
	price := PriceTier{
		Input:      p.Input,
		Output:     p.Output,
		CacheRead:  p.CacheRead,
		CacheWrite: p.CacheWrite,
	}
	for _, tier := range p.Tiers {
		if input > tier.InputAbove {
			price = tier
		}
	}
	return Cost{
		Input:      dollars(u.Input, price.Input),
		Output:     dollars(u.Output, price.Output),
		CacheRead:  dollars(u.CacheRead, price.CacheRead),
		CacheWrite: dollars(u.CacheWrite, price.CacheWrite),
	}
}

// dollars returns the cost of tokens at a price for one million tokens.
func dollars(tokens int, price float64) float64 {
	return float64(tokens) * price / tokensPerPriceUnit
}

// Cost is the cost of one model response in US dollars, by category of
// [Usage].
type Cost struct {
	Input      float64 `json:"input,omitempty"`
	Output     float64 `json:"output,omitempty"`
	CacheRead  float64 `json:"cache_read,omitempty"`
	CacheWrite float64 `json:"cache_write,omitempty"`
}

// Total returns the sum of the categories.
func (c Cost) Total() float64 {
	return c.Input + c.Output + c.CacheRead + c.CacheWrite
}

// Add returns the sum of two costs, category by category.
func (c Cost) Add(other Cost) Cost {
	return Cost{
		Input:      c.Input + other.Input,
		Output:     c.Output + other.Output,
		CacheRead:  c.CacheRead + other.CacheRead,
		CacheWrite: c.CacheWrite + other.CacheWrite,
	}
}

// Usage counts the tokens of one model response.
//
// The four categories do not overlap. Input counts the uncached input
// tokens only. There is no total, because providers do not agree on what
// belongs in one. A caller sums the fields that it needs.
//
// Cost is the cost of the tokens at the [Pricing] of the model. It is
// zero if the model has no prices.
//
// The JSON tags are the wire format. [Message] stores usage with them.
type Usage struct {
	Input      int  `json:"input,omitempty"`
	Output     int  `json:"output,omitempty"`
	CacheRead  int  `json:"cache_read,omitempty"`
	CacheWrite int  `json:"cache_write,omitempty"`
	Cost       Cost `json:"cost,omitzero"`
}

// Add returns the sum of two usage values, category by category. It is
// the accumulator for usage across turns and across runs.
func (u Usage) Add(other Usage) Usage {
	return Usage{
		Input:      u.Input + other.Input,
		Output:     u.Output + other.Output,
		CacheRead:  u.CacheRead + other.CacheRead,
		CacheWrite: u.CacheWrite + other.CacheWrite,
		Cost:       u.Cost.Add(other.Cost),
	}
}
