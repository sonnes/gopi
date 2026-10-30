package def

import (
	"context"
	"fmt"
)

// Set is the merged definitions of a list of readers. [Resolve] returns
// one.
type Set struct {
	// Skills holds the skills, as [MergeSkills] merges them.
	Skills []Skill
	// Names holds the name that the model sees for each skill of Skills,
	// as [SkillNames] gives it.
	Names []string
	// Agents holds the agents, as [MergeAgents] merges them.
	Agents []Agent
}

// HasSkill reports whether a skill has the name that the model sees, or
// the qualified name.
func (s Set) HasSkill(name string) bool {
	for i, skill := range s.Skills {
		if name == s.Names[i] || name == skill.QualifiedName() {
			return true
		}
	}
	return false
}

// Resolve reads skills and agents in order, and merges them. The
// harness and the drivers of a CLI read their lists with the same rules,
// so one list gives each driver the same names and the same winner for a
// clash.
func Resolve(ctx context.Context, skills []SkillsReader, agents []AgentsReader) (Set, error) {
	merged, err := MergeSkills(ctx, skills...)
	if err != nil {
		return Set{}, err
	}
	mergedAgents, err := MergeAgents(ctx, agents...)
	if err != nil {
		return Set{}, err
	}
	return Set{
		Skills: merged,
		Names:  SkillNames(merged),
		Agents: mergedAgents,
	}, nil
}

// MergeSkills reads each reader in order and merges the skills by
// qualified name. A later skill replaces an earlier skill with the same
// qualified name, at the position of the earlier one, so the list that
// the model sees keeps its order. An error of a reader fails the merge.
func MergeSkills(ctx context.Context, readers ...SkillsReader) ([]Skill, error) {
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

// SkillNames returns the name that the model sees for each skill. The
// last skill with a bare name gets the bare name, and the others get
// their qualified names. No skill is lost on a clash.
func SkillNames(skills []Skill) []string {
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

// MergeAgents reads each reader in order. A later agent replaces an
// earlier one with the same name, at its position. An error of a reader
// fails the merge.
func MergeAgents(ctx context.Context, readers ...AgentsReader) ([]Agent, error) {
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
