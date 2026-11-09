package def

import (
	"context"
	"fmt"
	"slices"
)

// Source gives definitions. A [Set] is a source of definitions in code,
// a store gives a source of a folder, and [Layers] merges sources. The
// harness calls Load each time that it assembles an agent, so an edit
// reaches the next agent. A slow source must cache on its own.
type Source interface {
	Load(ctx context.Context) (Set, error)
}

// Set holds the instructions, the skills, and the agents of a source.
type Set struct {
	// Instructions holds the instruction texts, in order.
	Instructions []Instruction
	// Skills holds the skills, in the order that the model sees them.
	// [Set.SkillNames] gives the name that the model sees for each.
	Skills []Skill
	// Agents holds the agents. If two agents have one name, the later
	// one wins.
	Agents []Agent
}

// Instruction is the text of one instruction file, such as AGENTS.md.
type Instruction struct {
	// Path is the place of the file, as the source shows it to the
	// model.
	Path string
	// Text is the content of the file.
	Text string
}

// Load returns a copy of s. A Set is then a source of definitions in
// code. The lists of the copy are new, so a caller can change them.
func (s Set) Load(context.Context) (Set, error) {
	return Set{
		Instructions: slices.Clone(s.Instructions),
		Skills:       slices.Clone(s.Skills),
		Agents:       slices.Clone(s.Agents),
	}, nil
}

// SkillNames returns the name that the model sees for each skill of
// Skills. The last skill with a bare name gets the bare name, and the
// others get their qualified names. No skill is lost on a clash.
func (s Set) SkillNames() []string {
	last := make(map[string]int, len(s.Skills))
	for i, skill := range s.Skills {
		last[skill.Name] = i
	}
	out := make([]string, len(s.Skills))
	for i, skill := range s.Skills {
		if last[skill.Name] == i {
			out[i] = skill.Name
			continue
		}
		out[i] = skill.QualifiedName()
	}
	return out
}

// Skill returns the skill with the name that the model sees, or with
// the qualified name, and reports whether it exists.
func (s Set) Skill(name string) (Skill, bool) {
	i := s.skillIndex(name)
	if i < 0 {
		return Skill{}, false
	}
	return s.Skills[i], true
}

// skillIndex returns the index of the skill with the name that the
// model sees, or with the qualified name, or -1.
func (s Set) skillIndex(name string) int {
	names := s.SkillNames()
	for i, skill := range s.Skills {
		if name == names[i] || name == skill.QualifiedName() {
			return i
		}
	}
	return -1
}

// Agent returns the last agent with the name, and reports whether it
// exists.
func (s Set) Agent(name string) (Agent, bool) {
	for _, v := range slices.Backward(s.Agents) {
		if v.Name == name {
			return v, true
		}
	}
	return Agent{}, false
}

// Layers returns a source that loads each source in order and merges
// the sets. A later source wins for a name, so a caller lists the
// sources lowest first:
//
//   - A later skill replaces an earlier skill with the same qualified
//     name, at the position of the earlier one. The list that the model
//     sees then keeps its order.
//   - A later agent replaces an earlier agent with the same name, at the
//     position of the earlier one.
//   - The instructions of each source follow the instructions of the
//     sources before it.
//
// A nil source gives nothing. An error of a source fails the load.
func Layers(sources ...Source) Source {
	return layers(sources)
}

// layers is the source that [Layers] returns.
type layers []Source

func (l layers) Load(ctx context.Context) (Set, error) {
	var (
		out     Set
		skillAt = make(map[string]int)
		agentAt = make(map[string]int)
	)
	for i, src := range l {
		if src == nil {
			continue
		}
		set, err := src.Load(ctx)
		if err != nil {
			return Set{}, fmt.Errorf("def: source %d (%T): %w", i, src, err)
		}
		out.Instructions = append(out.Instructions, set.Instructions...)
		for _, s := range set.Skills {
			key := s.QualifiedName()
			if pos, ok := skillAt[key]; ok {
				out.Skills[pos] = s
				continue
			}
			skillAt[key] = len(out.Skills)
			out.Skills = append(out.Skills, s)
		}
		for _, a := range set.Agents {
			if pos, ok := agentAt[a.Name]; ok {
				out.Agents[pos] = a
				continue
			}
			agentAt[a.Name] = len(out.Agents)
			out.Agents = append(out.Agents, a)
		}
	}
	return out, nil
}
