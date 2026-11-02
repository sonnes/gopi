package def

import (
	"context"
	"fmt"
)

// Set is the merged definitions of a list of readers. [Resolve] returns
// one.
type Set struct {
	// Skills holds the skills. A later skill replaces an earlier skill
	// with the same qualified name, at the position of the earlier one.
	Skills []Skill
	// Names holds the name that the model sees for each skill of Skills.
	// The last skill with a bare name gets the bare name, and the others
	// get their qualified names. No skill is lost on a clash.
	Names []string
	// Agents holds the agents. A later agent replaces an earlier one with
	// the same name, at its position.
	Agents []Agent
}

// Find returns the skill with the name that the model sees, or with the
// qualified name, and reports whether it exists.
func (s Set) Find(name string) (Skill, bool) {
	for i, skill := range s.Skills {
		if name == s.Names[i] || name == skill.QualifiedName() {
			return skill, true
		}
	}
	return Skill{}, false
}

// Resolve reads skills and agents in order, and merges them. The
// harness and the drivers of a CLI read their lists with the same rules,
// so one list gives each driver the same names and the same winner for a
// clash.
func Resolve(ctx context.Context, skills []SkillsReader, agents []AgentsReader) (Set, error) {
	merged, err := mergeSkills(ctx, skills...)
	if err != nil {
		return Set{}, err
	}
	mergedAgents, err := mergeAgents(ctx, agents...)
	if err != nil {
		return Set{}, err
	}
	return Set{
		Skills: merged,
		Names:  skillNames(merged),
		Agents: mergedAgents,
	}, nil
}

// mergeSkills reads each reader in order and merges the skills by
// qualified name. A later skill replaces an earlier skill with the same
// qualified name, at the position of the earlier one, so the list that
// the model sees keeps its order. An error of a reader fails the merge.
func mergeSkills(ctx context.Context, readers ...SkillsReader) ([]Skill, error) {
	var (
		out []Skill
		at  = make(map[string]int)
	)
	for i, r := range readers {
		skills, err := r.ReadSkills(ctx)
		if err != nil {
			return nil, fmt.Errorf("def: skills reader %d (%T): %w", i, r, err)
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
func skillNames(skills []Skill) []string {
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

// mergeAgents reads each reader in order. A later agent replaces an
// earlier one with the same name, at its position. An error of a reader
// fails the merge.
func mergeAgents(ctx context.Context, readers ...AgentsReader) ([]Agent, error) {
	var (
		out []Agent
		at  = make(map[string]int)
	)
	for i, r := range readers {
		agents, err := r.ReadAgents(ctx)
		if err != nil {
			return nil, fmt.Errorf("def: agents reader %d (%T): %w", i, r, err)
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
