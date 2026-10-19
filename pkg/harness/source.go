package harness

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/sonnes/gopi/pkg/harness/files"
	"github.com/sonnes/gopi/pkg/harness/skill"
)

// Source is a place that the harness reads instructions, skills, and
// subagents from. See [Sources].
type Source int

const (
	// User reads the home folder: the layouts of Claude Code, Codex,
	// and gopi.
	User Source = iota + 1
	// Project reads [Dir] and each parent of Dir up to the root of its
	// git repository. It also reads the skills of the subfolders of
	// Dir, with the path of the subfolder as their qualifier.
	Project
)

// userQualifier is the qualifier of a skill from the home folder.
const userQualifier = "~"

// nestedDepth is the deepest subfolder of [Dir] that [Project] reads
// skills from. A deeper scan reads more folders on each assembly.
const nestedDepth = 3

// skipFolders are the subfolders that [Project] does not scan for
// skills. They hold the code of other projects.
var skipFolders = []string{"node_modules", "vendor"}

// Each list is lowest first. In one folder, a skill of a later layout
// replaces a skill of an earlier layout with the same name, so .agents
// wins: it is the layout that is not for one tool.
var (
	skillLayouts = []string{".codex/skills", ".claude/skills", ".agents/skills"}
	agentLayouts = []string{".claude/agents", ".agents/agents"}
	projectFiles = []string{"CLAUDE.md", "AGENTS.md"}

	userSkills = []string{".codex/skills", ".claude/skills", ".agents/skills", ".gopi/skills"}
	userAgents = []string{".claude/agents", ".gopi/agents"}
	userFiles  = []string{".claude/CLAUDE.md", ".codex/AGENTS.md", ".gopi/AGENTS.md"}
)

// Instruction is the text of one instruction file, such as AGENTS.md.
type Instruction struct {
	// Path is the place of the file: relative to the root of the
	// project, or under "~/" for the home folder.
	Path string
	// Text is the content of the file.
	Text string
}

// sourced holds what the sources of one assembly give, lowest first.
type sourced struct {
	skills       []SkillsReader
	agents       []AgentsReader
	instructions []Instruction
}

// located is a skills reader whose skills get a qualifier.
type located struct {
	r   SkillsReader
	loc string
}

func (l located) ReadSkills(ctx context.Context) ([]skill.Skill, error) {
	skills, err := l.r.ReadSkills(ctx)
	if err != nil {
		return nil, err
	}
	for i := range skills {
		if skills[i].Qualifier == "" {
			skills[i].Qualifier = l.loc
		}
	}
	return skills, nil
}

// readSources reads the instruction files of the sources of h, and
// returns readers for their skills and subagents. The order is the
// home folder, the subfolders of dir, then each folder from the root of
// the project down to dir.
func (h *Harness) readSources(dir string) (sourced, error) {
	var out sourced
	want := h.sources
	if !h.sourcesSet {
		want = []Source{User, Project}
	}

	if slices.Contains(want, User) {
		if err := h.readUser(&out); err != nil {
			return sourced{}, err
		}
	}
	if slices.Contains(want, Project) {
		if err := readProject(dir, &out); err != nil {
			return sourced{}, err
		}
	}
	return out, nil
}

// readUser adds the layouts of the home folder to out. A missing home
// folder adds nothing.
func (h *Harness) readUser(out *sourced) error {
	home := h.home
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return nil
		}
	}
	fsys, ok, err := openFolder(home)
	if err != nil || !ok {
		return err
	}

	for _, name := range userFiles {
		if err := readInstruction(fsys, name, "~/"+name, out); err != nil {
			return err
		}
	}
	for _, d := range userSkills {
		out.skills = append(out.skills, located{r: files.Skills(fsys, d), loc: userQualifier})
	}
	for _, d := range userAgents {
		out.agents = append(out.agents, files.Agents(fsys, d))
	}
	return nil
}

// readProject adds the layouts of the project of dir to out.
func readProject(dir string, out *sourced) error {
	root := gitRoot(dir)
	fsys, ok, err := openFolder(root)
	if err != nil || !ok {
		return err
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return err
	}
	rel = filepath.ToSlash(rel)

	nested, err := subfolders(fsys, rel)
	if err != nil {
		return err
	}
	for _, f := range nested {
		for _, d := range skillLayouts {
			out.skills = append(out.skills, located{r: files.Skills(fsys, path.Join(f, d)), loc: f})
		}
	}

	for _, f := range chain(rel) {
		for _, name := range projectFiles {
			p := path.Join(f, name)
			if err := readInstruction(fsys, p, p, out); err != nil {
				return err
			}
		}
		for _, d := range skillLayouts {
			out.skills = append(out.skills, located{r: files.Skills(fsys, path.Join(f, d)), loc: f})
		}
		for _, d := range agentLayouts {
			out.agents = append(out.agents, files.Agents(fsys, path.Join(f, d)))
		}
	}
	return nil
}

// gitRoot returns the nearest folder at or above dir that holds a .git
// entry, or dir if there is none.
func gitRoot(dir string) string {
	for d := dir; ; {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return dir
		}
		d = parent
	}
}

// chain returns the folders from the root of the project down to rel,
// as paths relative to the root. The root is ".".
func chain(rel string) []string {
	out := []string{"."}
	if rel == "." {
		return out
	}
	cur := ""
	for _, part := range strings.Split(rel, "/") {
		cur = path.Join(cur, part)
		out = append(out, cur)
	}
	return out
}

// subfolders returns the subfolders of rel, up to [nestedDepth] levels
// down, in path order. It skips a hidden folder and each folder of
// [skipFolders].
func subfolders(fsys fs.FS, rel string) ([]string, error) {
	var out []string
	err := fs.WalkDir(fsys, rel, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == rel {
				return err
			}
			return nil
		}
		if !d.IsDir() || p == rel {
			return nil
		}
		name := d.Name()
		skip := name[0] == '.' || slices.Contains(skipFolders, name)
		if skip {
			return fs.SkipDir
		}
		out = append(out, p)
		if depth(rel, p) >= nestedDepth {
			return fs.SkipDir
		}
		return nil
	})
	return out, err
}

// depth returns how many levels p is below rel.
func depth(rel, p string) int {
	if rel != "." {
		p = p[len(rel)+1:]
	}
	return strings.Count(p, "/") + 1
}

// readInstruction adds the file name of fsys to out, shown as shown. A
// missing file adds nothing.
func readInstruction(fsys fs.FS, name, shown string, out *sourced) error {
	data, err := fs.ReadFile(fsys, name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("harness: read %s: %w", shown, err)
	}
	out.instructions = append(out.instructions, Instruction{Path: shown, Text: string(data)})
	return nil
}

// openFolder returns the FS of an [os.Root] at dir, or false if dir does
// not exist. An os.Root does not follow a symbolic link out of dir, so
// a link in a cloned repository cannot show the model a file outside
// it. The root closes when the FS is unreachable, because the skill
// tool reads the support files of a skill after the assembly.
func openFolder(dir string) (fs.FS, bool, error) {
	root, err := os.OpenRoot(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	fsys := &rootFS{FS: root.FS()}
	runtime.AddCleanup(fsys, func(r *os.Root) { r.Close() }, root)
	return fsys, true, nil
}

// rootFS is the FS of an [os.Root] that closes the root when it is
// unreachable.
type rootFS struct {
	fs.FS
}
