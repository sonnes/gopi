package files

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/sonnes/gopi/pkg/harness/skill"
)

// skillFile is the file that defines a skill in its folder.
const skillFile = "SKILL.md"

// forkContext is the value of the "context" key that runs a skill in a
// subagent.
const forkContext = "fork"

// descriptionSep divides the description from when_to_use in
// [skill.Skill.Description].
const descriptionSep = "\n\n"

var errNoDescription = errors.New("the skill has no description and no body")

// SkillDir reads skills from the folders in one directory of an fs.FS.
// [Skills] returns one.
type SkillDir struct {
	fsys fs.FS
	dir  string
}

// Skills returns a reader that reads one skill from each folder in dir
// that holds a SKILL.md file:
//
//	dir/<name>/SKILL.md  one skill; the other files of <name> are its support files
//
// The reader looks one level deep only, as Claude Code does. A folder
// with no SKILL.md is not a skill.
func Skills(fsys fs.FS, dir string) SkillDir {
	return SkillDir{
		fsys: fsys,
		dir:  dir,
	}
}

// ReadSkills reads each dir/<name>/SKILL.md in name order. The name of
// a skill is the name of its folder, so two skills of one dir never
// have the same name. If the frontmatter gives another name, the skill
// keeps it in Meta["name"]. ReadSkills skips a malformed file.
func (d SkillDir) ReadSkills(ctx context.Context) ([]skill.Skill, error) {
	entries, err := readDir(d.fsys, d.dir)
	if err != nil {
		return nil, err
	}

	var out []skill.Skill
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !e.IsDir() {
			continue
		}

		dir := path.Join(d.dir, e.Name())
		name := path.Join(dir, skillFile)
		data, err := fs.ReadFile(d.fsys, name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("files: read %s: %w", name, err)
		}

		s, err := parseSkill(e.Name(), data)
		if err != nil {
			continue
		}

		files, err := fs.Sub(d.fsys, dir)
		if err != nil {
			return nil, err
		}
		s.Files = files
		s.Path = name
		out = append(out, s)
	}
	return out, nil
}

// parseSkill reads a SKILL.md file in the folder name. A skill needs a
// description, because the model picks a skill by its description.
func parseSkill(name string, data []byte) (skill.Skill, error) {
	f, body, err := parseFile(data)
	if err != nil {
		return skill.Skill{}, err
	}

	rawName := f["name"]
	fileName, err := f.text("name")
	if err != nil {
		return skill.Skill{}, err
	}
	isOtherName := fileName != "" && fileName != name
	if isOtherName {
		f["name"] = rawName
	}

	description, err := f.text("description")
	if err != nil {
		return skill.Skill{}, err
	}
	if description == "" {
		description = firstLine(body)
	}
	whenToUse, err := f.text("when_to_use")
	if err != nil {
		return skill.Skill{}, err
	}
	description = joinNonEmpty(description, whenToUse)
	if description == "" {
		return skill.Skill{}, errNoDescription
	}

	allowed, _, err := f.list("allowed-tools")
	if err != nil {
		return skill.Skill{}, err
	}
	if len(allowed) == 0 {
		allowed = nil
	}
	model, err := f.text("model")
	if err != nil {
		return skill.Skill{}, err
	}
	agent, err := f.text("agent")
	if err != nil {
		return skill.Skill{}, err
	}
	noModelCall := f.flag("disable-model-invocation")
	fork := isFork(f)

	return skill.Skill{
		Name:         name,
		Description:  description,
		Body:         body,
		AllowedTools: allowed,
		Model:        model,
		NoModelCall:  noModelCall,
		Fork:         fork,
		Agent:        agent,
		Meta:         f.meta(),
	}, nil
}

// isFork reports whether the frontmatter says "context: fork". If it
// does, isFork removes the key. Another value of "context" stays in the
// frontmatter, so it reaches Meta.
func isFork(f fields) bool {
	v, ok := f["context"].(string)
	if !ok || strings.TrimSpace(v) != forkContext {
		return false
	}
	delete(f, "context")
	return true
}

// firstLine returns the first line of body that is not empty, without
// the # marks of a Markdown heading.
func firstLine(body string) string {
	for line := range strings.Lines(body) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		heading := strings.TrimLeft(line, "#")
		isHeading := heading != line && strings.HasPrefix(heading, " ")
		if isHeading {
			return strings.TrimSpace(heading)
		}
		return line
	}
	return ""
}

// joinNonEmpty joins the parts that are not empty with
// [descriptionSep].
func joinNonEmpty(parts ...string) string {
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, descriptionSep)
}

// readDir reads the entries of dir in name order. A dir that does not
// exist has no entries.
func readDir(fsys fs.FS, dir string) ([]fs.DirEntry, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("files: read %s: %w", dir, err)
	}
	return entries, nil
}
