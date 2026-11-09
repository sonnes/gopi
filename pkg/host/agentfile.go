package host

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/def"
	"github.com/sonnes/gopi/pkg/permission"
)

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

// checkMode returns an error if the mode of config is not a mode of
// gopi. Without it, an unknown mode fails only the next start.
func checkMode(config Config) error {
	_, err := permission.ParseMode(string(config.Mode))
	return err
}

// AgentSection is the name of the section with the prompt of the agent
// file of a session.
const AgentSection = "agent"

// findAgent returns the agent file of config in the definitions of
// config. A name that no agent has wraps [ErrInvalid].
func (h *Host) findAgent(ctx context.Context, config Config) (def.Agent, error) {
	set, err := h.configDefs(config).Load(ctx)
	if err != nil {
		return def.Agent{}, err
	}
	a, ok := set.Agent(config.Agent)
	if !ok {
		return def.Agent{}, fmt.Errorf("no agent file is named %q: %w", config.Agent, ErrInvalid)
	}
	return a, nil
}

// sectionsOf returns the function that gives the sections of the
// session id with config: the prompt of the agent file, then the
// sections of [Sections]. It returns nil if config has no agent file
// and the host has no sections, so the agent adds no prompt.
func (h *Host) sectionsOf(config Config, id string) agent.SystemFunc {
	fn := h.sections
	if config.Agent == "" && fn == nil {
		return nil
	}
	return func(ctx context.Context) ([]ai.Section, error) {
		var out []ai.Section
		if config.Agent != "" {
			a, err := h.findAgent(ctx, config)
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

// withAgent fills config from the file of config.Agent. A field that the
// config sets wins over the file. A config with no agent stays as it is.
func (h *Host) withAgent(ctx context.Context, config Config) (Config, error) {
	if config.Agent == "" {
		return config, nil
	}

	a, err := h.findAgent(ctx, config)
	if err != nil {
		return config, err
	}

	model := a.Model
	if a.InheritsModel() {
		model = ""
	}
	config.Model = cmp.Or(config.Model, model)
	config.Mode = cmp.Or(config.Mode, permission.Mode(a.Mode))
	config.Effort = cmp.Or(config.Effort, a.Effort)
	if config.ToolNames == nil {
		config.ToolNames = a.Tools
	}
	config.Deny = append(slices.Clip(config.Deny), a.Disallowed...)
	config.Skills = append(slices.Clip(config.Skills), a.Skills...)
	return config, nil
}
