package gemini

import (
	"fmt"

	"google.golang.org/genai/interactions/models/interactions"

	"github.com/sonnes/gopi/pkg/ai"
)

// A cut-off output stops with Length, also when it holds a tool call,
// because the arguments of a cut-off call can be partial. A status of a
// run that did not end, failed, or that a caller cancelled is an error.
func stopReason(status interactions.InteractionStatus, hasToolCall bool) (ai.StopReason, error) {
	switch status {
	case interactions.InteractionStatusIncomplete,
		interactions.InteractionStatusBudgetExceeded:
		return ai.StopReasonLength, nil
	case interactions.InteractionStatusRequiresAction:
		return ai.StopReasonToolUse, nil
	case interactions.InteractionStatusCompleted:
		if hasToolCall {
			return ai.StopReasonToolUse, nil
		}
		return ai.StopReasonStop, nil
	default:
		return "", fmt.Errorf("gemini: interaction %s", status)
	}
}

func textParts(contents []interactions.Content) []string {
	var parts []string
	for _, c := range contents {
		if c.TextContent != nil {
			parts = append(parts, c.TextContent.Text)
		}
	}
	return parts
}

// The cached tokens are a part of the input tokens. Google bills the
// thought tokens as output.
func usageOf(u *interactions.Usage, model ai.Model) ai.Usage {
	if u == nil {
		return ai.Usage{}
	}
	cached := value(u.TotalCachedTokens)
	usage := ai.Usage{
		Input:     value(u.TotalInputTokens) - cached,
		CacheRead: cached,
		Output:    value(u.TotalOutputTokens) + value(u.TotalThoughtTokens),
	}
	usage.Cost = model.Pricing.Cost(usage)
	return usage
}

func value(n *int) int {
	if n == nil {
		return 0
	}
	return *n
}
