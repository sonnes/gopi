package host

import (
	"cmp"
	"context"
	"slices"
	"strings"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/def"
	storefs "github.com/sonnes/gopi/pkg/store/fs"
)

// inheritModel is the model of an agent file that uses the model of
// the config.
const inheritModel = "inherit"

// checkEffort returns an error if the effort of config is not a thinking
// level, so that a typo in an agent file fails the start and not the
// first model call.
func checkEffort(config Config) error {
	if config.Effort == "" {
		return nil
	}
	_, err := ai.ParseThinking(string(config.Effort))
	return err
}

// withAgent fills config from the file of config.Agent in the dirs. A field
// that the config sets wins over the file. A config with no agent stays as
// it is.
func withAgent(ctx context.Context, config Config) (Config, error) {
	if config.Agent == "" {
		return config, nil
	}

	readers := make([]def.AgentsReader, len(config.Dirs))
	for i, d := range config.Dirs {
		readers[i] = storefs.Dir(d)
	}
	a, err := def.FindAgent(ctx, config.Agent, readers...)
	if err != nil {
		return config, err
	}

	model := a.Model
	if strings.EqualFold(model, inheritModel) {
		model = ""
	}
	config.Model = cmp.Or(config.Model, model)
	config.Mode = cmp.Or(config.Mode, agent.Mode(a.Mode))
	config.Effort = cmp.Or(config.Effort, a.Effort)
	config.System = joinText(a.Prompt, config.System)
	config.Deny = append(slices.Clip(config.Deny), a.Disallowed...)
	config.Skills = append(slices.Clip(config.Skills), a.Skills...)
	return config, nil
}

// joinText returns the texts that are not empty, joined by a blank
// line.
func joinText(texts ...string) string {
	var parts []string
	for _, t := range texts {
		if t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "\n\n")
}
