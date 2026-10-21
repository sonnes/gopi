package codex

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/sonnes/gopi/pkg/ai"
)

// listTimeout is the longest time that a list of models waits for the
// app server. An app server that waits for a login would block the list
// until the ctx of the caller ends. It is a variable so that a test can
// make it short.
var listTimeout = 30 * time.Second

// listModels starts the app server of c, and lists its models with
// model/list, page by page. It skips a hidden model, and puts the
// default model first. The list fails if the app server does not
// answer in [listTimeout]. A failed list kills the app server, because
// a stuck server does not exit when its input ends.
func listModels(ctx context.Context, c config) ([]ai.Model, error) {
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	pipes, err := start(appServer(c))
	if err != nil {
		return nil, fmt.Errorf("codex: list models: %w", err)
	}
	conn := dial(pipes, nil)
	models, err := listPages(ctx, conn)
	if err != nil {
		conn.kill()
		return nil, fmt.Errorf("codex: list models: %w", err)
	}
	conn.close()
	return models, nil
}

// listPages initializes conn and returns the models of each page of
// model/list. A cursor that repeats is an error, so a server that sends
// the same cursor on each page does not keep the list in a loop.
func listPages(ctx context.Context, conn *conn) ([]ai.Model, error) {
	if err := conn.initialize(ctx); err != nil {
		return nil, err
	}
	var models []ai.Model
	var params modelListParams
	seen := make(map[string]bool)
	for {
		var page modelListResult
		if err := conn.call(ctx, "model/list", params, &page); err != nil {
			return nil, err
		}
		for _, m := range page.Data {
			if m.Hidden {
				continue
			}
			if m.IsDefault {
				models = slices.Insert(models, 0, m.model())
				continue
			}
			models = append(models, m.model())
		}
		if page.NextCursor == "" {
			return models, nil
		}
		if seen[page.NextCursor] {
			return nil, fmt.Errorf("the cursor %q repeats", page.NextCursor)
		}
		seen[page.NextCursor] = true
		params.Cursor = page.NextCursor
	}
}

// inputModalities maps an input modality of the CLI to an
// [ai.Modality].
var inputModalities = map[string]ai.Modality{
	"text":  ai.ModalityText,
	"image": ai.ModalityImage,
}

// efforts maps a reasoning effort of the CLI to an [ai.ThinkingLevel].
// "ultra" has no level.
var efforts = map[string]ai.ThinkingLevel{
	"none":   ai.ThinkingOff,
	"low":    ai.ThinkingLow,
	"medium": ai.ThinkingMedium,
	"high":   ai.ThinkingHigh,
	"xhigh":  ai.ThinkingXHigh,
	"max":    ai.ThinkingMax,
}

// model returns m as an [ai.Model].
func (m listedModel) model() ai.Model {
	model := ai.Model{
		ID:   m.ID,
		Name: m.DisplayName,
	}
	for _, name := range m.InputModalities {
		if mod, ok := inputModalities[name]; ok {
			model.Input = append(model.Input, mod)
		}
	}
	for _, e := range m.SupportedReasoningEfforts {
		if level, ok := efforts[e.ReasoningEffort]; ok {
			model.ThinkingLevels = append(model.ThinkingLevels, level)
		}
	}
	model.Reasoning = len(m.SupportedReasoningEfforts) > 0
	return model
}
