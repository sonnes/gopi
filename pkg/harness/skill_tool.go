package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/harness/skill"
)

// skillToolName is the name of the skill tool.
const skillToolName = "skill"

// skillFile is the file that defines a skill. The skill tool does not
// list it as a support file, because its body is the result.
const skillFile = "SKILL.md"

// maxListedFiles caps the support files that the skill tool lists after
// a body, so a skill with a large tree does not fill the context.
const maxListedFiles = 50

const skillToolDescription = "Load the instructions of a skill. " +
	"Give file to read a support file of the skill, such as a script or an example."

// skillTool is the tool that loads a skill. An assembly with skills
// adds it after the tools of [Toolset]. The schema lists the skill
// names, and the result is the body of the skill, then the paths of its
// support files. With the file input, the result is one support file.
//
// The body arrives as a tool result, not in the system prompt. So the
// prompt stays the same for the whole session, and a provider can cache
// it.
type skillTool struct {
	// names holds the name that the model sees for each skill, in order.
	names []string
	// byName finds a skill by the name that the model sees, and by its
	// qualified name.
	byName map[string]skill.Skill
}

// newSkillTool returns the skill tool for skills, which the model sees
// as names. It leaves out a skill that the model cannot call. With no
// skill left, it returns nil.
func newSkillTool(skills []skill.Skill, names []string) *skillTool {
	t := &skillTool{byName: make(map[string]skill.Skill, len(skills))}
	for i, s := range skills {
		if s.NoModelCall {
			continue
		}
		t.names = append(t.names, names[i])
		t.byName[names[i]] = s
		t.byName[s.QualifiedName()] = s
	}
	if len(t.names) == 0 {
		return nil
	}
	return t
}

// Info returns the info of the skill tool. The name input lists the
// skill names as an enum, so the model cannot ask for a skill that does
// not exist.
func (t *skillTool) Info() ai.ToolInfo {
	name := &jsonschema.Schema{
		Type:        "string",
		Description: "The name of the skill.",
	}
	for _, n := range t.names {
		name.Enum = append(name.Enum, n)
	}
	file := &jsonschema.Schema{
		Type:        "string",
		Description: "The path of a support file, relative to the skill. Leave it out to load the instructions.",
	}
	return ai.ToolInfo{
		Name:        skillToolName,
		Description: skillToolDescription,
		InputSchema: &jsonschema.Schema{
			Type: "object",
			Properties: map[string]*jsonschema.Schema{
				"name": name,
				"file": file,
			},
			Required: []string{"name"},
		},
	}
}

type skillInput struct {
	Name string `json:"name"`
	File string `json:"file"`
}

// Run answers one call of the skill tool. Every failure is an error
// result and no error, because an error stops the run of the agent, and
// the model can correct the call.
func (t *skillTool) Run(_ context.Context, call ai.ToolCall) (ai.ToolResult, error) {
	var in skillInput
	if err := json.Unmarshal(call.Input, &in); err != nil {
		return errorResult(fmt.Sprintf("invalid input: %s", err)), nil
	}

	s, ok := t.byName[in.Name]
	if !ok {
		text := fmt.Sprintf("no skill is named %q. The skills are: %s.", in.Name, strings.Join(t.names, ", "))
		return errorResult(text), nil
	}

	if in.File == "" {
		return textResult(skillBody(s)), nil
	}
	return readSkillFile(s, in.File), nil
}

// skillBody returns the body of s, then the paths of its support files.
func skillBody(s skill.Skill) string {
	files := supportFiles(s.Files)
	if len(files) == 0 {
		return s.Body
	}

	var b strings.Builder
	b.WriteString(s.Body)
	b.WriteString("\n\nSupport files of this skill. To read one, call this tool with the file input:\n")
	for _, f := range files {
		b.WriteString("\n- ")
		b.WriteString(f)
	}
	return b.String()
}

// supportFiles returns the paths of the files in fsys, other than
// SKILL.md, up to maxListedFiles.
func supportFiles(fsys fs.FS) []string {
	if fsys == nil {
		return nil
	}
	var out []string
	_ = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || p == skillFile {
			return nil
		}
		if len(out) == maxListedFiles {
			return fs.SkipAll
		}
		out = append(out, p)
		return nil
	})
	return out
}

// readSkillFile returns the support file name of s.
func readSkillFile(s skill.Skill, name string) ai.ToolResult {
	missing := fmt.Sprintf("the skill %q has no file %q", s.Name, name)
	if s.Files == nil || !fs.ValidPath(name) {
		return errorResult(missing)
	}
	data, err := fs.ReadFile(s.Files, name)
	if err != nil {
		return errorResult(missing)
	}
	return textResult(string(data))
}

func textResult(s string) ai.ToolResult {
	return ai.ToolResult{Content: []ai.Content{ai.Text{Text: s}}}
}

func errorResult(s string) ai.ToolResult {
	return ai.ToolResult{
		Content: []ai.Content{ai.Text{Text: s}},
		IsError: true,
	}
}
