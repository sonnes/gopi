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

// The parts of a folder that [Dir] and [FS] read.
const (
	agentsDir        = "agents"
	skillsDir        = "skills"
	instructionsFile = "AGENTS.md"
)

// config holds the options of [Dir] and [FS].
type config struct {
	instructions []string
}

// Option configures [Dir] and [FS].
type Option func(*config)

// InstructionFiles sets the names of the instruction files of the
// folder, in order. A missing file gives nothing. Without
// InstructionFiles, the folder has the one file AGENTS.md. A caller
// that wants the file of Claude Code gives "CLAUDE.md".
func InstructionFiles(names ...string) Option {
	return func(c *config) { c.instructions = names }
}

func newConfig(opts []Option) config {
	c := config{instructions: []string{instructionsFile}}
	for _, opt := range opts {
		opt(&c)
	}
	return c
}

// FS returns a source of the definitions in fsys:
//
//	agents/**/*.md     one agent from each file, at any depth
//	skills/*/SKILL.md  one skill from each folder; the other files of
//	                   the folder are its support files
//	AGENTS.md          the instruction text, or the files of
//	                   [InstructionFiles]
//
// An [embed.FS] of files that ship in the binary then reads the same as
// a folder on disk. A missing part gives nothing. The path of each
// definition is its path in fsys. To read another layout, such as
// .claude, give [io/fs.Sub] of it.
func FS(fsys fs.FS, opts ...Option) def.Source {
	return fsSource{
		fsys: fsys,
		cfg:  newConfig(opts),
	}
}

// fsSource is the source that [FS] returns.
type fsSource struct {
	fsys fs.FS
	cfg  config
}

func (s fsSource) Load(ctx context.Context) (def.Set, error) {
	agents, err := readAgents(ctx, s.fsys, agentsDir)
	if err != nil {
		return def.Set{}, err
	}
	skills, err := readSkills(ctx, s.fsys, skillsDir)
	if err != nil {
		return def.Set{}, err
	}
	instructions, err := readInstructions(s.fsys, s.cfg.instructions)
	if err != nil {
		return def.Set{}, err
	}
	return def.Set{
		Instructions: instructions,
		Skills:       skills,
		Agents:       agents,
	}, nil
}

// readInstructions reads each file of names in fsys, in order. A missing
// file gives nothing.
func readInstructions(fsys fs.FS, names []string) ([]def.Instruction, error) {
	var out []def.Instruction
	for _, name := range names {
		data, err := fs.ReadFile(fsys, name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("fs: read %s: %w", name, err)
		}
		out = append(out, def.Instruction{
			Path: name,
			Text: string(data),
		})
	}
	return out, nil
}

// Dir returns a source of the folder on disk at path, in the layout of
// [FS]. A missing folder gives nothing. Each load opens the folder
// again, so an edit or a folder that appears later reaches the next
// load. The path of each definition is the path on disk. The support
// files of a skill stay inside the folder: a symbolic link out of it
// does not open.
func Dir(path string, opts ...Option) def.Source {
	return dirSource{
		path: path,
		opts: opts,
	}
}

// dirSource is the source that [Dir] returns.
type dirSource struct {
	path string
	opts []Option
}

func (d dirSource) Load(ctx context.Context) (def.Set, error) {
	fsys, ok, err := openRoot(d.path)
	if err != nil || !ok {
		return def.Set{}, err
	}
	set, err := FS(fsys, d.opts...).Load(ctx)
	if err != nil {
		return def.Set{}, fmt.Errorf("fs: %s: %w", d.path, err)
	}
	for i := range set.Agents {
		set.Agents[i].Path = d.onDisk(set.Agents[i].Path)
	}
	for i := range set.Skills {
		set.Skills[i].Path = d.onDisk(set.Skills[i].Path)
	}
	for i := range set.Instructions {
		set.Instructions[i].Path = d.onDisk(set.Instructions[i].Path)
	}
	return set, nil
}

// onDisk returns the path on disk of name, a slash path in the folder.
func (d dirSource) onDisk(name string) string {
	return filepath.Join(d.path, filepath.FromSlash(name))
}

// openRoot returns the FS of an [os.Root] at dir, or false if dir does
// not exist. An os.Root does not follow a symbolic link out of dir, so
// a link in a cloned repository cannot show the model a file outside
// it. The root closes when the FS is unreachable, because the skill
// tool reads the support files of a skill after the load.
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
