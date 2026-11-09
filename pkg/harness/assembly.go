package harness

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/compact"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/def"
	"github.com/sonnes/gopi/pkg/sandbox"
	"github.com/sonnes/gopi/pkg/session"
	"github.com/sonnes/gopi/pkg/tool"
)

// Assembly is what one assembly resolved. The prompt builder and the
// seeders read it. Only a builder gets one, so a caller cannot resolve
// an assembly without an agent.
type Assembly struct {
	// Spec is the model spec of the assembly.
	Spec string
	// Model is the model that Spec resolves to.
	Model ai.Model
	// System is the base text that [System] set, with the body of each
	// skill of the Skills of the config.
	System string
	// Instructions holds the instruction texts of the source of [Defs],
	// then of the Defs of the config, in order.
	Instructions []def.Instruction
	// Tools describes the tools that the model can call, in the order of
	// [Toolset], then the Tools of the config, then the MCP tools. A
	// deferred tool is not in the list, because the model cannot call it
	// until the search of the agent loads it.
	Tools []ai.ToolInfo
	// Skills holds the skills of the sources, merged by qualified name.
	// [Assembly.SkillName] gives the name that the model sees.
	Skills []def.Skill
	// Agents holds the subagents, merged by name. A child assembly has
	// none, so a subagent cannot start a subagent.
	Agents []def.Agent
	// Dir is the folder that the agent works in.
	Dir string

	// h is the harness of the call that the assembly came from.
	h *Harness
	// sessionID is the ID of the session, or "" for an unsaved agent.
	sessionID string
	// defs holds the merged definitions.
	defs def.Set
	// names holds the name that the model sees for each skill.
	names []string
	// tools are the tools that go to the agent, deferred or not.
	tools []tool.Tool
	// box is the sandbox of the tools of [Sandboxed]. It is nil until
	// the first of them binds.
	box sandbox.Sandbox
}

// SkillName returns the name that the model gives for skill i. It is
// the bare name of the skill if no later skill has the same bare name.
// Else it is the qualified name, such as "apps/web:deploy".
func (a *Assembly) SkillName(i int) string {
	return a.names[i]
}

// skill returns the skill that has the name or the qualified name, and
// reports whether it exists.
func (a *Assembly) skill(name string) (def.Skill, bool) {
	return a.defs.Skill(name)
}

// withSkills returns text with the body of each named skill after it.
// If no skill has a name, it returns an error.
func (a *Assembly) withSkills(text string, names []string) (string, error) {
	parts := []string{text}
	for _, name := range names {
		s, ok := a.skill(name)
		if !ok {
			return "", fmt.Errorf("harness: preload the skill %q: no skill has this name", name)
		}
		parts = append(parts, skillText(name, s))
	}
	return joinText(parts...), nil
}

// skillText is the text of a preloaded skill in the prompt.
func skillText(name string, s def.Skill) string {
	return skillHeading(name) + s.Body
}

// skillHeading is the heading of the body of the skill name in a text.
func skillHeading(name string) string {
	return "## Skill: " + name + "\n\n"
}

// agentTool reports if the agent tool binds to the assembly.
func (a *Assembly) agentTool() bool {
	return len(a.Agents) > 0 && a.h.child == nil
}

// hasTool reports if the assembly has a tool with the name, deferred or
// not.
func (a *Assembly) hasTool(name string) bool {
	for _, t := range a.tools {
		if t.Info().Name == name {
			return true
		}
	}
	return false
}

// assemble loads the definitions of h, reads its tools, and fills in an
// Assembly for the spec of h. id is the ID of the session, or "".
func (h *Harness) assemble(ctx context.Context, id string) (*Assembly, error) {
	model, err := h.model()
	if err != nil {
		return nil, err
	}
	dir, err := h.workDir()
	if err != nil {
		return nil, err
	}
	set, err := def.Layers(h.defs, h.cfg.Defs).Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("harness: %w", err)
	}
	// A subagent gets no agents of its parent.
	if h.child != nil {
		set.Agents = nil
	}

	asm := &Assembly{
		Spec:         h.cfg.Spec,
		Model:        model,
		System:       h.system,
		Instructions: set.Instructions,
		Skills:       set.Skills,
		Agents:       set.Agents,
		Dir:          dir,
		h:            h,
		sessionID:    id,
		defs:         set,
		names:        set.SkillNames(),
	}
	if h.child == nil {
		// A subagent preloads the skills of its file, in childSystem,
		// and not the skills of its parent.
		asm.System, err = asm.withSkills(asm.System, h.cfg.Skills)
		if err != nil {
			return nil, err
		}
	}
	asm.tools, err = asm.readTools(ctx)
	if err != nil {
		return nil, err
	}
	for _, t := range asm.tools {
		if info := t.Info(); !info.Deferred {
			asm.Tools = append(asm.Tools, info)
		}
	}
	if err := h.policy(dir).Validate(); err != nil {
		return nil, fmt.Errorf("harness: %w", err)
	}
	return asm, nil
}

// model returns the model of the spec of h in its catalog.
func (h *Harness) model() (ai.Model, error) {
	if h.cat == nil {
		return ai.Model{}, errors.New("harness: no catalog; give one with Catalog")
	}
	if h.cfg.Spec == "" {
		return ai.Model{}, errors.New("harness: no model; give a Spec in the config")
	}
	model, err := h.cat.Model(h.cfg.Spec)
	if err != nil {
		return ai.Model{}, fmt.Errorf("harness: model %q: %w", h.cfg.Spec, err)
	}
	return model, nil
}

// workDir returns the absolute path of the Dir of the config, or the
// working directory.
func (h *Harness) workDir() (string, error) {
	if h.cfg.Dir == "" {
		return os.Getwd()
	}
	return filepath.Abs(h.cfg.Dir)
}

// readTools returns the tools of the assembly: the tools of each reader
// of [Toolset] in order, the Tools of the config, and the MCP tools,
// each bound to the assembly. In a child assembly, it keeps only the
// tools of the subagent. With the ToolNames of the config, it keeps
// only the named tools.
//
// It picks the tools by name before it binds them. A name of the list
// then passes ToolNames also when its tool binds to nothing, such as
// the skill tool in a folder with no skill, and a tool that ToolNames
// leaves out makes no sandbox.
func (a *Assembly) readTools(ctx context.Context) ([]tool.Tool, error) {
	var read []tool.Tool
	for i, r := range a.h.tools {
		tools, err := r.ReadTools(ctx)
		if err != nil {
			return nil, fmt.Errorf("harness: tools reader %d (%T): %w", i, r, err)
		}
		read = append(read, tools...)
	}
	read = append(read, a.h.cfg.Tools...)
	read = append(read, a.h.mcpTools...)
	read, err := a.pick(read)
	if err != nil {
		return nil, err
	}
	out, err := a.bindTools(read)
	if err != nil {
		return nil, err
	}
	if err := uniqueNames(out); err != nil {
		return nil, err
	}
	return out, nil
}

// pick returns the tools of tools that the ToolNames of the config
// keeps, without each tool that a Deny rule with no content names, and
// in a child assembly the tools of the subagent. A name of ToolNames or
// of the file of the subagent that names no tool fails with
// [tool.ErrUnknownTool], so a typo does not remove a tool silently. A
// child narrows the names of ToolNames with no check: the start of its
// parent checked them, and the file of the subagent removes some of
// them, such as the agent tool.
func (a *Assembly) pick(tools []tool.Tool) ([]tool.Tool, error) {
	only := a.h.cfg.ToolNames
	if c := a.h.child; c != nil {
		if _, err := tool.Select(tools, c.sub.Tools); err != nil {
			return nil, fmt.Errorf("harness: subagent %q: %w", c.sub.Name, err)
		}
		tools = keepTools(tools, c.sub)
		if only != nil {
			tools = keepTools(tools, def.Agent{Tools: only})
		}
		return withoutDenied(tools, a.h.cfg.Deny), nil
	}
	if only != nil {
		kept, err := tool.Select(tools, only)
		if err != nil {
			return nil, fmt.Errorf("harness: %w", err)
		}
		tools = kept
	}
	return withoutDenied(tools, a.h.cfg.Deny), nil
}

// withoutDenied returns the tools without each tool that a deny rule
// with no content names, as Claude Code removes it. A rule with
// content, such as "bash(git push *)", keeps the tool, and the policy
// denies the calls that the rule matches.
func withoutDenied(tools []tool.Tool, rules []string) []tool.Tool {
	whole := tool.WholeTools(rules)
	if len(whole) == 0 {
		return tools
	}
	return keepTools(tools, def.Agent{Disallowed: whole})
}

// uniqueNames returns an error if two tools have one name. The
// assembly then fails, and the agent does not fail later.
func uniqueNames(tools []tool.Tool) error {
	names := make(map[string]bool, len(tools))
	for _, t := range tools {
		name := t.Info().Name
		if names[name] {
			return fmt.Errorf("harness: two tools are named %q", name)
		}
		names[name] = true
	}
	return nil
}

// prepare resolves the language model. It returns the agent options in
// the order that the harness promises: the permission hook, the hook
// that loads the skill of a slash call, the options of [AgentDefaults],
// the options of the config, then the prompt and the tools. The agent
// builds the prompt before each model call, through [agent.System]: the
// section of [Assembly.System], the sections of the prompt builder,
// then the sections of the config.
func (a *Assembly) prepare() (ai.LanguageModel, []agent.Option, error) {
	lm, err := a.h.cat.LanguageModel(a.Spec)
	if err != nil {
		return ai.LanguageModel{}, nil, fmt.Errorf("harness: model %q: %w", a.Spec, err)
	}

	build := a.h.prompt
	if build == nil {
		build = DefaultPrompt
	}
	system := func(ctx context.Context) ([]ai.Section, error) {
		built, err := build(ctx, a)
		if err != nil {
			return nil, fmt.Errorf("harness: build the prompt: %w", err)
		}
		if a.System != "" {
			built = append([]ai.Section{{Name: SystemSection, Text: a.System}}, built...)
		}
		sections := a.h.cfg.Sections
		if sections == nil {
			return built, nil
		}
		more, err := sections(ctx)
		if err != nil {
			return nil, fmt.Errorf("harness: build the prompt: %w", err)
		}
		return append(built, more...), nil
	}

	approve := a.h.cfg.Approve
	opts := []agent.Option{
		agent.Hooks(policyHook(a.h.policy(a.Dir), approve)),
		agent.Approve(approve),
		// The first option, so [AgentDefaults] can replace it.
		agent.Compaction(compact.Summarize()),
	}
	if a.h.child != nil && a.h.child.sub.MaxTurns > 0 {
		opts = append(opts, agent.Hooks(agent.MaxTurns(a.h.child.sub.MaxTurns)))
	}
	// Only a user writes the input of a parent. A subagent gets the
	// text of the model, and the model cannot load a skill this way.
	if a.h.child == nil && len(a.Skills) > 0 {
		opts = append(opts, agent.Hooks(a.slashSkills()))
	}
	opts = append(opts, a.h.agentOpts...)
	opts = append(opts, configOptions(a.h.cfg)...)
	opts = append(opts, agent.System(system))
	opts = append(opts, agent.Toolset(a.tools...))
	return lm, opts, nil
}

// seed appends the entries of the seeders to log if log has no
// entries. It runs the seeders in order, and appends all of their
// entries at once, so a failed seeder leaves the log empty.
func (a *Assembly) seed(ctx context.Context, log *session.Log) error {
	if len(log.Entries()) > 0 {
		return nil
	}
	seeders := a.h.seeds
	if len(seeders) == 0 {
		seeders = []Seeder{DefaultSeed}
	}

	var entries []session.Entry
	for _, build := range seeders {
		if build == nil {
			continue
		}
		next, err := build(ctx, a)
		if err != nil {
			return fmt.Errorf("harness: build the seed: %w", err)
		}
		entries = append(entries, next...)
	}
	if len(entries) == 0 {
		return nil
	}
	_, err := log.Append(ctx, entries...)
	return err
}
