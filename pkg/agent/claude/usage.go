package claude

import (
	"context"

	"github.com/sonnes/gopi/pkg/agent/internal/resume"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
)

// wireModelUsage is one entry of modelUsage on a result line. The CLI
// sums each entry over the life of its process, so the usage of one run
// is the difference from the result line of the run before.
type wireModelUsage struct {
	InputTokens              int     `json:"inputTokens"`
	OutputTokens             int     `json:"outputTokens"`
	CacheReadInputTokens     int     `json:"cacheReadInputTokens"`
	CacheCreationInputTokens int     `json:"cacheCreationInputTokens"`
	CostUSD                  float64 `json:"costUSD"`
}

// usageBase is the modelUsage of the last result line of a conversation
// of the CLI. A CLI that resumes the conversation starts its sums from
// these values, so the next run takes the difference from them.
type usageBase struct {
	Conversation string                    `json:"conversation"`
	Models       map[string]wireModelUsage `json:"models"`
}

// baseValue keeps the [usageBase] of the agent in its session.
var baseValue = resume.Value[usageBase](cliName, "model_usage")

// loadBase returns the modelUsage that the CLI of conversation starts
// from. A new conversation starts from zero.
func loadBase(r session.Reader, conversation string) (map[string]wireModelUsage, error) {
	if conversation == "" {
		return nil, nil
	}
	b, _, err := baseValue.Get(r)
	if err != nil || b.Conversation != conversation {
		return nil, err
	}
	return b.Models, nil
}

// saveBase stores the modelUsage of the result line of conversation. The
// write ignores the cancel of ctx, because the CLI counted the usage
// either way.
func saveBase(ctx context.Context, w session.Writer, conversation string, models map[string]wireModelUsage) error {
	if conversation == "" {
		return nil
	}
	b := usageBase{Conversation: conversation, Models: models}
	return baseValue.Set(context.WithoutCancel(ctx), w, b)
}

// The price of each kind of token as a part of the input price, for
// most Claude models. The CLI reports one cost for each model, and these
// weights split it into the parts of [ai.Cost]. The parts sum to the
// cost of the CLI. A model with other ratios gets parts that are close.
const (
	weightInput      = 1
	weightOutput     = 5
	weightCacheRead  = 0.1
	weightCacheWrite = 1.25
)

// runUsage returns the usage of each model of one run, from the
// modelUsage of its result line. It keeps cur as the base of the next
// run of the process. A model that the run did not call has no key.
func (p *process) runUsage(cur map[string]wireModelUsage) map[string]ai.Usage {
	out := map[string]ai.Usage{}
	for model, c := range cur {
		prev := p.models[model]
		u := ai.Usage{
			Input:      c.InputTokens - prev.InputTokens,
			Output:     c.OutputTokens - prev.OutputTokens,
			CacheRead:  c.CacheReadInputTokens - prev.CacheReadInputTokens,
			CacheWrite: c.CacheCreationInputTokens - prev.CacheCreationInputTokens,
		}
		if u == (ai.Usage{}) {
			continue
		}
		u.Cost = splitCost(u, c.CostUSD-prev.CostUSD)
		out[model] = u
	}
	p.models = cur
	return out
}

// splitCost splits total over the token kinds of u by their weights.
// The parts sum to total.
func splitCost(u ai.Usage, total float64) ai.Cost {
	in := float64(u.Input) * weightInput
	out := float64(u.Output) * weightOutput
	read := float64(u.CacheRead) * weightCacheRead
	write := float64(u.CacheWrite) * weightCacheWrite
	sum := in + out + read + write
	if total <= 0 || sum == 0 {
		return ai.Cost{}
	}
	return ai.Cost{
		Input:      total * in / sum,
		Output:     total * out / sum,
		CacheRead:  total * read / sum,
		CacheWrite: total * write / sum,
	}
}
