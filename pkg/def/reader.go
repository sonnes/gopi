package def

import "context"

// AgentsReader reads agents. The harness calls ReadAgents each time
// that it assembles an agent, so an edit reaches the next agent. A slow
// reader must cache on its own. An [Agent] is an AgentsReader.
type AgentsReader interface {
	ReadAgents(ctx context.Context) ([]Agent, error)
}

// SkillsReader reads skills. The harness calls ReadSkills each time
// that it assembles an agent. A [Skill] is a SkillsReader.
type SkillsReader interface {
	ReadSkills(ctx context.Context) ([]Skill, error)
}

// InstructionsReader reads instruction texts. The harness calls
// ReadInstructions each time that it assembles an agent.
type InstructionsReader interface {
	ReadInstructions(ctx context.Context) ([]Instruction, error)
}

// Reader reads agents, skills, and instruction texts. A folder of a
// store is a Reader.
type Reader interface {
	AgentsReader
	SkillsReader
	InstructionsReader
}

// Instruction is the text of one instruction file, such as AGENTS.md.
type Instruction struct {
	// Path is the place of the file, as the reader shows it to the
	// model.
	Path string
	// Text is the content of the file.
	Text string
}

// ReadInstructions returns i as the one instruction of a reader. An
// Instruction is then an instructions reader, and an instruction in
// code goes in the list of readers.
func (i Instruction) ReadInstructions(context.Context) ([]Instruction, error) {
	return []Instruction{i}, nil
}
