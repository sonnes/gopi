package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/sonnes/gopi/pkg/ai"
)

// defaultAlias is the alias of the default model of the CLI. Its name,
// such as "Default (recommended)", does not name the model that it
// resolves to.
const defaultAlias = "default"

// listTimeout is the longest time that a list of models waits for the
// CLI. A CLI that waits for a login, or a hook that never ends, would
// block the list until the ctx of the caller ends. It is a variable so
// that a test can make it short.
var listTimeout = 30 * time.Second

// initResponse is the part of the initialize response that lists the
// models of the CLI.
type initResponse struct {
	Models []listedModel `json:"models"`
}

// listedModel is one model of the initialize response. Value is what
// --model takes: an alias, such as "opus", or a full ID.
type listedModel struct {
	Value                 string   `json:"value"`
	ResolvedModel         string   `json:"resolvedModel"`
	DisplayName           string   `json:"displayName"`
	SupportedEffortLevels []string `json:"supportedEffortLevels"`
	SupportsThinkingOff   bool     `json:"supportsThinkingOff"`
}

// efforts maps an effort level of the CLI to an [ai.ThinkingLevel].
var efforts = map[string]ai.ThinkingLevel{
	"low":    ai.ThinkingLow,
	"medium": ai.ThinkingMedium,
	"high":   ai.ThinkingHigh,
	"xhigh":  ai.ThinkingXHigh,
	"max":    ai.ThinkingMax,
}

// listModels starts the CLI of c, sends the initialize request, and
// returns the models of its response. The CLI runs no model call. The
// list fails if the CLI does not answer in [listTimeout].
func listModels(ctx context.Context, c config) ([]ai.Model, error) {
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	args, err := cliArgs(c, "")
	if err != nil {
		return nil, fmt.Errorf("claude: list models: %w", err)
	}
	proc, err := launch(cliCommand(c, args), nil, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("claude: list models: %w", err)
	}
	body, err := proc.initialize(ctx)
	// A list keeps no session, so the CLI stops at once. How it exits
	// does not change a list that arrived.
	proc.terminate()
	if err != nil {
		return nil, fmt.Errorf("claude: list models: %w", err)
	}
	var resp initResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("claude: list models: %w", err)
	}
	return cliModels(resp.Models), nil
}

// cliModels returns each alias and full ID of listed, in its order, then
// each model that an alias resolves to and listed does not have. A
// resolved model takes the name of the first alias other than
// [defaultAlias] that resolves to it.
func cliModels(listed []listedModel) []ai.Model {
	var out []ai.Model
	for _, m := range listed {
		out = append(out, m.model(m.Value))
	}

	// The default alias comes last, so that a resolved model takes the
	// name of another alias if one exists.
	byName := slices.Clone(listed)
	slices.SortStableFunc(byName, func(a, b listedModel) int {
		return boolOrder(a.Value == defaultAlias, b.Value == defaultAlias)
	})
	for _, m := range byName {
		known := slices.ContainsFunc(out, func(o ai.Model) bool {
			return o.ID == m.ResolvedModel
		})
		if m.ResolvedModel == "" || known {
			continue
		}
		out = append(out, m.model(m.ResolvedModel))
	}
	return out
}

// boolOrder orders false before true.
func boolOrder(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	default:
		return -1
	}
}

// model returns m as an [ai.Model] with the ID id.
func (m listedModel) model(id string) ai.Model {
	var levels []ai.ThinkingLevel
	if m.SupportsThinkingOff {
		levels = append(levels, ai.ThinkingOff)
	}
	for _, name := range m.SupportedEffortLevels {
		if level, ok := efforts[name]; ok {
			levels = append(levels, level)
		}
	}
	return ai.Model{
		ID:             id,
		Name:           m.DisplayName,
		Reasoning:      len(levels) > 0,
		ThinkingLevels: levels,
	}
}
