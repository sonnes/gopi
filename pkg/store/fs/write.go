package fs

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v4"

	"github.com/sonnes/gopi/pkg/def"
)

const (
	// writeDirPerm keeps a written folder private to the user.
	writeDirPerm = 0o700
	// writeFilePerm keeps a written file private to the user.
	writeFilePerm = 0o600
	// ownerExec is the bit of a file that the owner can run.
	ownerExec = 0o100
)

// WriteSkills writes each skill to dir, in the format of the skills
// folder of [FS]: dir/<name>/SKILL.md and the support files of the
// skill. The name of a skill is the name at its index in names, which is
// the name that the model sees, as [def.Set.SkillNames] gives it. A name
// that [CheckName] rejects fails the write. A support file that is a
// symbolic link and does not open stays out.
func WriteSkills(dir string, skills []def.Skill, names []string) error {
	for i, s := range skills {
		name := names[i]
		if err := CheckName(name); err != nil {
			return err
		}
		folder := filepath.Join(dir, name)
		if err := os.MkdirAll(folder, writeDirPerm); err != nil {
			return fmt.Errorf("fs: write the skill %s: %w", name, err)
		}
		if s.Files != nil {
			if err := copyFiles(folder, s.Files); err != nil {
				return fmt.Errorf("fs: write the skill %s: %w", name, err)
			}
		}
		data, err := skillData(name, s)
		if err != nil {
			return fmt.Errorf("fs: write the skill %s: %w", name, err)
		}
		file := filepath.Join(folder, skillFile)
		if err := os.WriteFile(file, data, writeFilePerm); err != nil {
			return fmt.Errorf("fs: write the skill %s: %w", name, err)
		}
	}
	return nil
}

// WriteAgents writes each agent to dir, in the format of the agents
// folder of [FS]: dir/<name>.md. A name that [CheckName] rejects fails
// the write.
func WriteAgents(dir string, agents []def.Agent) error {
	if err := os.MkdirAll(dir, writeDirPerm); err != nil {
		return fmt.Errorf("fs: write the agents: %w", err)
	}
	for _, a := range agents {
		if err := CheckName(a.Name); err != nil {
			return err
		}
		data, err := agentData(a)
		if err != nil {
			return fmt.Errorf("fs: write the agent %s: %w", a.Name, err)
		}
		file := filepath.Join(dir, a.Name+agentExt)
		if err := os.WriteFile(file, data, writeFilePerm); err != nil {
			return fmt.Errorf("fs: write the agent %s: %w", a.Name, err)
		}
	}
	return nil
}

// CheckName returns an error if name is not one element of a path. A
// file with the name then stays in its folder. A writer of definitions
// fails on such a name, because a name comes from a file of a folder
// that a person can clone.
func CheckName(name string) error {
	isOne := name != "" &&
		name != "." &&
		name != ".." &&
		!strings.ContainsAny(name, `/\`)
	if !isOne {
		return fmt.Errorf("fs: the name %q is not a file name", name)
	}
	return nil
}

// skillData returns the text of the SKILL.md file of s. The name key is
// name, so the frontmatter name is the name of the folder.
func skillData(name string, s def.Skill) ([]byte, error) {
	keys := maps.Clone(s.Extra)
	if keys == nil {
		keys = def.Extra{}
	}
	keys["name"] = name
	keys["description"] = s.Description
	if len(s.AllowedTools) > 0 {
		keys["allowed-tools"] = s.AllowedTools
	}
	if s.Model != "" {
		keys["model"] = s.Model
	}
	if s.NoModelCall {
		keys["disable-model-invocation"] = true
	}
	if s.Fork {
		keys["context"] = forkContext
	}
	if s.Agent != "" {
		keys["agent"] = s.Agent
	}
	return fileData(keys, s.Body)
}

// agentData returns the text of the file of a. A nil Tools list has no
// key, and an empty list has an empty key, as [FS] reads them.
func agentData(a def.Agent) ([]byte, error) {
	keys := maps.Clone(a.Extra)
	if keys == nil {
		keys = def.Extra{}
	}
	keys["name"] = a.Name
	keys["description"] = a.Description
	if a.Tools != nil {
		keys["tools"] = a.Tools
	}
	if len(a.Disallowed) > 0 {
		keys["disallowedTools"] = a.Disallowed
	}
	if a.Model != "" {
		keys["model"] = a.Model
	}
	if a.Effort != "" {
		keys["effort"] = string(a.Effort)
	}
	if a.Mode != "" {
		keys["permissionMode"] = a.Mode
	}
	if a.MaxTurns > 0 {
		keys["maxTurns"] = a.MaxTurns
	}
	if len(a.Skills) > 0 {
		keys["skills"] = a.Skills
	}
	return fileData(keys, a.Prompt)
}

// fileData returns keys as YAML frontmatter, then body.
func fileData(keys def.Extra, body string) ([]byte, error) {
	meta, err := yaml.Marshal(map[string]any(keys))
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.WriteString(fence + "\n")
	b.Write(meta)
	b.WriteString(fence + "\n\n")
	b.WriteString(body)
	b.WriteString("\n")
	return b.Bytes(), nil
}

// copyFiles copies each file of files to folder, without the SKILL.md
// file at the top. A file that the owner can run stays so.
func copyFiles(folder string, files fs.FS) error {
	walk := func(name string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() || name == skillFile {
			return nil
		}
		isLink := e.Type()&fs.ModeSymlink != 0

		data, err := fs.ReadFile(files, name)
		if err != nil {
			if isLink {
				return nil
			}
			return err
		}
		perm := fs.FileMode(writeFilePerm)
		info, err := fs.Stat(files, name)
		if err == nil && info.Mode().Perm()&ownerExec != 0 {
			perm |= ownerExec
		}

		target := filepath.Join(folder, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), writeDirPerm); err != nil {
			return err
		}
		return os.WriteFile(target, data, perm)
	}
	err := fs.WalkDir(files, ".", walk)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
