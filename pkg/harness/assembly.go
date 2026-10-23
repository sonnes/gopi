package harness

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/def"
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
	// System is the base text that [System] set.
	System string
	// Instructions holds the instruction texts of the readers of
	// [Instructions] and [Defs], in order.
	Instructions []def.Instruction
	// Tools describes the tools that the model can call, in order: the
	// built-in tools, the tools of [Toolset], the skill tool, then the
	// task tool. A deferred tool is not in the list, because the model
	// cannot call it until the search of the agent loads it.
	Tools []ai.ToolInfo
	// Skills holds the skills of the readers, merged by qualified name. [Assembly.SkillName] gives the name that the model
	// sees.
	Skills []def.Skill
	// Agents holds the subagents, merged by name. A child assembly has
	// none, so a subagent cannot start a subagent.
	Agents []def.Agent
	// Dir is the folder that the agent works in.
	Dir string

	// h is the harness of the call that the assembly came from.
	h *Harness
	// session is the ID of the session, or "" for an unsaved agent.
	session string
	// names holds the name that the model sees for each skill.
	names []string
	// tools are the tools that go to the agent, deferred or not.
	tools []tool.Tool
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
	for i, s := range a.Skills {
		if name == a.SkillName(i) || name == s.QualifiedName() {
			return s, true
		}
	}
	return def.Skill{}, false
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
	return "## Skill: " + name + "\n\n" + s.Body
}

// taskTool reports if the assembly gives the model the task tool.
func (a *Assembly) taskTool() bool {
	return len(a.Agents) > 0 && a.h.child == nil
}

// assemble reads the readers of h, and fills in an
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
	instructions, err := readInstructions(ctx, h.instructions)
	if err != nil {
		return nil, err
	}

	asm := &Assembly{
		Spec:         h.spec,
		Model:        model,
		System:       h.system,
		Instructions: instructions,
		Dir:          dir,
		h:            h,
		session:      id,
	}
	asm.Skills, err = readSkills(ctx, h.skills)
	if err != nil {
		return nil, err
	}
	asm.names = skillNames(asm.Skills)
	if h.child == nil {
		// A subagent preloads the skills of its file, in childSystem,
		// and not the skills of its parent.
		asm.System, err = asm.withSkills(asm.System, h.preload)
		if err != nil {
			return nil, err
		}
		asm.Agents, err = readAgents(ctx, h.agents)
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
	if err := h.policy.Validate(); err != nil {
		return nil, fmt.Errorf("harness: %w", err)
	}
	return asm, nil
}

// model returns the model of the spec of h in its catalog.
func (h *Harness) model() (ai.Model, error) {
	if h.cat == nil {
		return ai.Model{}, errors.New("harness: no catalog; give one with Catalog")
	}
	if h.spec == "" {
		return ai.Model{}, errors.New("harness: no model; give a spec with Model")
	}
	model, err := h.cat.Model(h.spec)
	if err != nil {
		return ai.Model{}, fmt.Errorf("harness: model %q: %w", h.spec, err)
	}
	return model, nil
}

// workDir returns the absolute path of [Dir], or the working directory.
func (h *Harness) workDir() (string, error) {
	if h.dir == "" {
		return os.Getwd()
	}
	return filepath.Abs(h.dir)
}

// readInstructions reads each instructions reader in order. An error of
// a reader fails the assembly.
func readInstructions(ctx context.Context, readers []def.InstructionsReader) ([]def.Instruction, error) {
	var out []def.Instruction
	for i, r := range readers {
		texts, err := r.ReadInstructions(ctx)
		if err != nil {
			return nil, fmt.Errorf("harness: instructions reader %d (%T): %w", i, r, err)
		}
		out = append(out, texts...)
	}
	return out, nil
}

// readSkills reads each skills reader in order and merges the skills by
// qualified name. A later skill replaces an earlier skill with the same
// qualified name, at the position of the earlier one, so the list that
// the model sees keeps its order. An error of a reader fails the
// assembly.
func readSkills(ctx context.Context, readers []def.SkillsReader) ([]def.Skill, error) {
	var (
		out []def.Skill
		at  = make(map[string]int)
	)
	for i, r := range readers {
		skills, err := r.ReadSkills(ctx)
		if err != nil {
			return nil, fmt.Errorf("harness: skills reader %d (%T): %w", i, r, err)
		}
		for _, s := range skills {
			key := s.QualifiedName()
			if pos, ok := at[key]; ok {
				out[pos] = s
				continue
			}
			at[key] = len(out)
			out = append(out, s)
		}
	}
	return out, nil
}

// skillNames returns the name that the model sees for each skill. The
// last skill with a bare name gets the bare name, and the others get
// their qualified names. No skill is lost on a clash.
func skillNames(skills []def.Skill) []string {
	last := make(map[string]int, len(skills))
	for i, s := range skills {
		last[s.Name] = i
	}
	out := make([]string, len(skills))
	for i, s := range skills {
		if last[s.Name] == i {
			out[i] = s.Name
			continue
		}
		out[i] = s.QualifiedName()
	}
	return out
}

// readAgents reads each agents reader in order. A later subagent
// replaces an earlier one with the same name, at its position.
func readAgents(ctx context.Context, readers []def.AgentsReader) ([]def.Agent, error) {
	var (
		out []def.Agent
		at  = make(map[string]int)
	)
	for i, r := range readers {
		agents, err := r.ReadAgents(ctx)
		if err != nil {
			return nil, fmt.Errorf("harness: agents reader %d (%T): %w", i, r, err)
		}
		for _, a := range agents {
			if pos, ok := at[a.Name]; ok {
				out[pos] = a
				continue
			}
			at[a.Name] = len(out)
			out = append(out, a)
		}
	}
	return out, nil
}

// readTools returns the tools of the assembly: the built-in tools, the
// tools of each reader in order, the skill tool, then the task tool.
// In a child assembly, it keeps only the tools of the subagent.
func (a *Assembly) readTools(ctx context.Context) ([]tool.Tool, error) {
	out, err := a.h.builtinTools(a.Dir)
	if err != nil {
		return nil, err
	}
	for i, r := range a.h.tools {
		tools, err := r.ReadTools(ctx)
		if err != nil {
			return nil, fmt.Errorf("harness: tools reader %d (%T): %w", i, r, err)
		}
		out = append(out, tools...)
	}
	if t := newSkillTool(a.Skills, a.names); t != nil {
		out = append(out, t)
	}
	if a.taskTool() {
		out = append(out, newTaskTool(a))
	}
	if a.h.child != nil {
		out = keepTools(out, a.h.child.sub)
	}
	if err := uniqueNames(out); err != nil {
		return nil, err
	}
	return out, nil
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
// the order that the harness promises: the permission hook, the agent
// options of the baseline and of the call, then the prompt and the
// tools. The agent builds the prompt before each model call, through
// [agent.System]: the section of [Assembly.System], then the sections
// of the prompt builder.
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
		if a.System == "" {
			return built, nil
		}
		return append([]ai.Section{{Name: SystemSection, Text: a.System}}, built...), nil
	}

	policy := a.h.policy
	policy.Dir = a.Dir
	opts := []agent.Option{agent.Hooks(policy.Hook())}
	if a.h.child != nil && a.h.child.sub.MaxTurns > 0 {
		opts = append(opts, maxTurns(a.h.child.sub.MaxTurns))
	}
	opts = append(opts, a.h.agentOpts...)
	opts = append(opts, agent.System(system))
	opts = append(opts, agent.Toolset(a.tools...))
	return lm, opts, nil
}

// maxTurns returns a hook that fails a run after n turns.
func maxTurns(n int) agent.Option {
	return agent.BeforeCall(func(_ context.Context, s agent.State, r agent.Request) (agent.Request, error) {
		if s.Turn() > n {
			return r, fmt.Errorf("harness: the subagent used more than %d turns", n)
		}
		return r, nil
	})
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
