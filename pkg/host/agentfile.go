package host

import (
	"cmp"
	"context"
	"fmt"
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

// AgentSection is the name of the section with the prompt of the agent
// file of a session.
const AgentSection = "agent"

// findAgent returns the agent file of config in its dirs.
func findAgent(ctx context.Context, config Config) (def.Agent, error) {
	readers := make([]def.AgentsReader, len(config.Dirs))
	for i, d := range config.Dirs {
		readers[i] = storefs.Dir(d)
	}
	return def.FindAgent(ctx, config.Agent, readers...)
}

// sectionsOf returns the function that gives the sections of the
// session id with config: the prompt of the agent file, then the
// sections of fn. It returns nil if config has no agent file and fn
// is nil, so the agent adds no prompt.
func sectionsOf(config Config, id string, fn SectionsFunc) agent.SystemFunc {
	if config.Agent == "" && fn == nil {
		return nil
	}
	return func(ctx context.Context) ([]ai.Section, error) {
		var out []ai.Section
		if config.Agent != "" {
			a, err := findAgent(ctx, config)
			if err != nil {
				return nil, fmt.Errorf("host: session %s: %w", id, err)
			}
			if a.Prompt != "" {
				out = append(out, ai.Section{Name: AgentSection, Text: a.Prompt})
			}
		}
		if fn != nil {
			more, err := fn(ctx, id)
			if err != nil {
				return nil, fmt.Errorf("host: session %s: the sections: %w", id, err)
			}
			out = append(out, more...)
		}
		return out, nil
	}
}

// withAgent fills config from the file of config.Agent in the dirs. A field
// that the config sets wins over the file. A config with no agent stays as
// it is.
func withAgent(ctx context.Context, config Config) (Config, error) {
	if config.Agent == "" {
		return config, nil
	}

	a, err := findAgent(ctx, config)
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
	config.Deny = append(slices.Clip(config.Deny), a.Disallowed...)
	config.Skills = append(slices.Clip(config.Skills), a.Skills...)
	return config, nil
}
