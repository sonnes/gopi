package fs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"github.com/sonnes/gopi/pkg/def"
)

// The parts of a folder that [Dir] reads.
const (
	agentsDir        = "agents"
	skillsDir        = "skills"
	instructionsFile = "AGENTS.md"
)

// Folder reads the definitions of one folder on disk. [Dir] returns
// one.
type Folder struct {
	path         string
	instructions []string
}

// DirOption configures a [Folder].
type DirOption func(*Folder)

// InstructionFiles sets the names of the instruction files of the
// folder, in order. A missing file gives nothing. Without
// InstructionFiles, the folder has the one file AGENTS.md. A caller
// that wants the file of Claude Code gives "CLAUDE.md".
func InstructionFiles(names ...string) DirOption {
	return func(f *Folder) { f.instructions = names }
}

var _ def.Reader = (*Folder)(nil)

// Dir returns a reader of the folder at path:
//
//	<path>/agents/**/*.md     the agents, as [Agents] reads them
//	<path>/skills/*/SKILL.md  the skills, as [Skills] reads them
//	<path>/AGENTS.md          the instruction text, or the files of
//	                          [InstructionFiles]
//
// A missing folder or a missing part gives nothing. Each read opens
// the folder again, so an edit or a folder that appears later reaches
// the next read. The path of each definition is the path on disk.
func Dir(path string, opts ...DirOption) *Folder {
	f := &Folder{
		path:         path,
		instructions: []string{instructionsFile},
	}
	for _, opt := range opts {
		opt(f)
	}
	return f
}

// ReadAgents reads the agents of the folder.
func (f *Folder) ReadAgents(ctx context.Context) ([]def.Agent, error) {
	fsys, ok, err := openRoot(f.path)
	if err != nil || !ok {
		return nil, err
	}
	agents, err := Agents(fsys, agentsDir).ReadAgents(ctx)
	if err != nil {
		return nil, err
	}
	for i := range agents {
		agents[i].Path = f.onDisk(agents[i].Path)
	}
	return agents, nil
}

// ReadSkills reads the skills of the folder. The support files of a
// skill stay inside the folder: a symbolic link out of it does not
// open.
func (f *Folder) ReadSkills(ctx context.Context) ([]def.Skill, error) {
	fsys, ok, err := openRoot(f.path)
	if err != nil || !ok {
		return nil, err
	}
	skills, err := Skills(fsys, skillsDir).ReadSkills(ctx)
	if err != nil {
		return nil, err
	}
	for i := range skills {
		skills[i].Path = f.onDisk(skills[i].Path)
	}
	return skills, nil
}

// ReadInstructions reads each instruction file of the folder, in order.
func (f *Folder) ReadInstructions(context.Context) ([]def.Instruction, error) {
	fsys, ok, err := openRoot(f.path)
	if err != nil || !ok {
		return nil, err
	}
	var out []def.Instruction
	for _, name := range f.instructions {
		data, err := fs.ReadFile(fsys, name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("fs: read %s: %w", f.onDisk(name), err)
		}
		out = append(out, def.Instruction{
			Path: f.onDisk(name),
			Text: string(data),
		})
	}
	return out, nil
}

// onDisk returns the path on disk of name, a slash path in the folder.
func (f *Folder) onDisk(name string) string {
	return filepath.Join(f.path, filepath.FromSlash(name))
}

// openRoot returns the FS of an [os.Root] at dir, or false if dir does
// not exist. An os.Root does not follow a symbolic link out of dir, so
// a link in a cloned repository cannot show the model a file outside
// it. The root closes when the FS is unreachable, because the skill
// tool reads the support files of a skill after the read.
func openRoot(dir string) (fs.FS, bool, error) {
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
