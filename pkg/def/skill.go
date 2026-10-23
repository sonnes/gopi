package def

import (
	"context"
	"io/fs"
)

// qualifierSep divides the qualifier from the name in
// [Skill.QualifiedName].
const qualifierSep = ":"

// Skill is a named body of instructions that the model loads on demand.
// The system prompt holds only the name and the description. The body
// reaches the model as the result of the skill tool, so the prompt does
// not change when the model loads a skill.
//
// A reader of SKILL.md files sets the fields from the frontmatter. The
// doc comment of each field names its frontmatter key.
type Skill struct {
	// Name identifies the skill. The model gives it to the skill tool.
	// A reader of files sets it to the name of the skill folder. In
	// code, the caller sets it.
	Name string
	// Qualifier tells two skills with the same name apart, such as
	// "ops" in "ops:deploy". The caller sets it. A reader of files
	// leaves it empty, so a later skill with the same name replaces an
	// earlier one.
	Qualifier string
	// Description tells the model when to load the skill. A reader sets
	// it to "description", then "\n\n" and "when_to_use" if the file
	// has it. If "description" is empty, the reader uses the first line
	// of the body that is not empty.
	Description string
	// Body is the text of the skill, without the frontmatter.
	Body string
	// Files holds the support files of the skill, such as scripts and
	// examples. The skill tool reads a file from it by path. Files is
	// nil if the skill has no support files.
	Files fs.FS
	// Path is the location of the SKILL.md file, for a reader. It is
	// empty for a skill in code.
	Path string
	// AllowedTools lists the tools that the skill can use without a
	// permission prompt. The frontmatter key is "allowed-tools". It is
	// nil by default.
	AllowedTools []string
	// Model is the model that runs the skill. The frontmatter key is
	// "model". If it is empty, the skill uses the model of the agent.
	Model string
	// NoModelCall stops the model from loading the skill. Only the user
	// can then load it. The frontmatter key is
	// "disable-model-invocation". It is false by default.
	NoModelCall bool
	// Fork runs the skill in a subagent with its own context. The
	// frontmatter sets it with "context: fork". It is false by default.
	Fork bool
	// Agent names the subagent that runs a fork. The frontmatter key is
	// "agent". If it is empty, the harness picks the subagent.
	Agent string
	// Extra holds each frontmatter key that no field takes, such as
	// "metadata", "license", "hooks", "paths", and "user-invocable". It
	// also holds "name" if the frontmatter name is not the folder name.
	// It is nil if there are no other keys.
	Extra Extra
}

// ReadSkills returns s as the one skill of a reader. A Skill is then a
// skills reader, and a skill in code goes in the list of readers.
func (s Skill) ReadSkills(context.Context) ([]Skill, error) {
	return []Skill{s}, nil
}

// QualifiedName returns the qualifier, a colon, and the name, such as
// "apps/web:deploy". If the qualifier is empty, it returns the name.
func (s Skill) QualifiedName() string {
	if s.Qualifier == "" {
		return s.Name
	}
	return s.Qualifier + qualifierSep + s.Name
}
