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
	// System is the base text that [System] set.
	System string
	// Instructions holds the instruction texts of the readers of
	// [Instructions] and [Defs], in order.
	Instructions []def.Instruction
	// Tools describes the tools that the model can call, in the order of
	// [Toolset]. A deferred tool is not in the list, because the model
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
	// sessionID is the ID of the session, or "" for an unsaved agent.
	sessionID string
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
	set := def.Set{
		Skills: a.Skills,
		Names:  a.names,
	}
	return set.Find(name)
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

// agentTool reports if the assembly gives the model the agent tool.
func (a *Assembly) agentTool() bool {
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
		sessionID:    id,
	}
	// A subagent gets no agents of its parent.
	agents := h.agents
	if h.child != nil {
		agents = nil
	}
	set, err := def.Resolve(ctx, h.skills, agents)
	if err != nil {
		return nil, err
	}
	asm.Skills = set.Skills
	asm.names = set.Names
	asm.Agents = set.Agents
	if h.child == nil {
		// A subagent preloads the skills of its file, in childSystem,
		// and not the skills of its parent.
		asm.System, err = asm.withSkills(asm.System, h.preload)
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

// readTools returns the tools of the assembly: the tools of each reader
// of [Toolset] in order, each bound to the assembly. In a child
// assembly, it keeps only the tools of the subagent. With [Only], it
// keeps only the named tools.
//
// It picks the tools by name before it binds them. A name of the list
// then passes [Only] also when its tool binds to nothing, such as the
// skill tool in a folder with no skill, and a tool that Only leaves out
// makes no sandbox.
func (a *Assembly) readTools(ctx context.Context) ([]tool.Tool, error) {
	var read []tool.Tool
	for i, r := range a.h.tools {
		tools, err := r.ReadTools(ctx)
		if err != nil {
			return nil, fmt.Errorf("harness: tools reader %d (%T): %w", i, r, err)
		}
		read = append(read, tools...)
	}
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

// pick returns the tools of tools that [Only] keeps, without each tool
// that a [Deny] rule with no content names, and in a child assembly
// the tools of the subagent. A name of Only or of the file of the
// subagent that names no tool fails with [tool.ErrUnknownTool], so a
// typo does not remove a tool silently. A child narrows the names of
// Only with no check: the start of its parent checked them, and the
// file of the subagent removes some of them, such as the agent tool.
func (a *Assembly) pick(tools []tool.Tool) ([]tool.Tool, error) {
	if c := a.h.child; c != nil {
		if _, err := tool.Select(tools, c.sub.Tools); err != nil {
			return nil, fmt.Errorf("harness: subagent %q: %w", c.sub.Name, err)
		}
		tools = keepTools(tools, c.sub)
		if a.h.only != nil {
			tools = keepTools(tools, def.Agent{Tools: a.h.only})
		}
		return withoutDenied(tools, a.h.policy.Deny), nil
	}
	if a.h.only != nil {
		kept, err := tool.Select(tools, a.h.only)
		if err != nil {
			return nil, fmt.Errorf("harness: %w", err)
		}
		tools = kept
	}
	return withoutDenied(tools, a.h.policy.Deny), nil
}

// withoutDenied returns the tools without each tool that a deny rule
// with no content names, as Claude Code removes it. A rule with
// content, such as "bash(git push *)", keeps the tool, and the policy
// denies the calls that the rule matches.
func withoutDenied(tools []tool.Tool, rules []string) []tool.Tool {
	var whole []string
	for _, r := range rules {
		if _, content := tool.SplitRule(r); content == "" {
			whole = append(whole, r)
		}
	}
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
// that loads the skill of a slash call, the agent options of the
// baseline and of the call, then the prompt and the tools. The agent builds the prompt before each model call, through
// [agent.System]: the section of [Assembly.System], the sections of the
// prompt builder, then the sections of [Sections].
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
		if a.h.sections == nil {
			return built, nil
		}
		more, err := a.h.sections(ctx)
		if err != nil {
			return nil, fmt.Errorf("harness: build the prompt: %w", err)
		}
		return append(built, more...), nil
	}

	policy := a.h.policy
	policy.Dir = a.Dir
	policy.Roots = append([]string{a.Dir}, a.h.addDirs...)
	opts := []agent.Option{
		agent.Hooks(policy.Hook()),
		// The first option, so [AgentDefaults] can replace it.
		agent.Compaction(compact.Summarize()),
	}
	if a.h.child != nil && a.h.child.sub.MaxTurns > 0 {
		opts = append(opts, maxTurns(a.h.child.sub.MaxTurns))
	}
	// Only a user writes the input of a parent. A subagent gets the
	// text of the model, and the model cannot load a skill this way.
	if a.h.child == nil && len(a.Skills) > 0 {
		opts = append(opts, a.slashSkills())
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
