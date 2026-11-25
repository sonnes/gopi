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
	"github.com/sonnes/gopi/pkg/tool"
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

// SkillRulesSection is the name of the section with the rules to use the
// skills of a session. The name is not "skills", because the harness
// lists the skills in a section of that name.
const SkillRulesSection = "skill-rules"

// SkillRulesPrompt is the text of the section [SkillRulesSection]. It
// holds only the rules, because each driver lists the skills itself.
// The host gives it to each driver, so a session of a CLI gets the same
// rules as a session of the harness.
const SkillRulesPrompt = `The runtime lists each skill with one line.
Before you reply, read the list of skills.
If a skill matches the job, even in part, load the skill and obey it.
Load the skill even for a job that you know how to do. The skill holds the method of this workspace for that job.
Do not use a skill that is not in the list.
If a skill that you used had a wrong step, or did not have a step, correct the skill before you end the run.
After a hard job with many tries, offer to save the method as a skill.`

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
// session id with config: the prompt of the agent file, then
// [SkillRulesSection] if skillRules is true, then the sections of
// [Sections]. It returns nil if config has no agent file, the host has
// no sections, and skillRules is false, so the agent adds no prompt.
func (h *Host) sectionsOf(config Config, id string, skillRules bool) agent.SystemFunc {
	fn := h.sections
	if config.Agent == "" && fn == nil && !skillRules {
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
		if skillRules {
			out = append(out, ai.Section{Name: SkillRulesSection, Text: SkillRulesPrompt})
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

// skillRules reports whether a session of config gets
// [SkillRulesSection]: the session keeps the skill tool, and the model
// can call one or more of its skills. These are the conditions of the
// skills section of the harness, so the prompt never gives the rules
// for a tool that the model does not have. The host reads the skills
// once, at the start of the agent, and not before each model call. An
// edit of the skills then changes the rules at the next start, as it
// changes the skills of each driver.
func (h *Host) skillRules(ctx context.Context, config Config) (bool, error) {
	if !h.keepsTool(config, tool.Skill) {
		return false, nil
	}
	skills, err := h.ConfigSkills(ctx, config)
	if err != nil {
		return false, err
	}
	callable := slices.ContainsFunc(skills, func(s def.Skill) bool {
		return !s.NoModelCall
	})
	return callable, nil
}

// keepsTool reports whether a session of config keeps the tool name of
// the host: the ToolNames of config keep it, and no Deny rule with no
// content removes it, as each driver picks the tools.
func (h *Host) keepsTool(config Config, name string) bool {
	kept, err := tool.Select(h.tools, config.ToolNames)
	if err != nil {
		return false
	}
	var denied []string
	for _, rule := range tool.WholeTools(config.Deny) {
		denied = append(denied, tool.RuleNames(rule)...)
	}
	for _, t := range kept {
		if t.Info().Name == name {
			return !slices.Contains(denied, name)
		}
	}
	return false
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
