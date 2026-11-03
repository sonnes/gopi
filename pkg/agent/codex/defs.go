package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/sonnes/gopi/pkg/agent/internal/clidefs"
	"github.com/sonnes/gopi/pkg/def"
	storefs "github.com/sonnes/gopi/pkg/store/fs"
)

// The folders under the folder of the session, and the extension of a
// role file.
const (
	skillsDir = "skills"
	rolesDir  = "codex/agents"
	roleExt   = ".toml"
)

// The permissions of a role file and its folder.
const (
	rolesDirPerm = 0o700
	roleFilePerm = 0o600
)

// writeDefs writes the skills and the roles of the session. It returns
// the merged set, the folder of the skills, and the agents table of the
// next thread. A thread reads a role from its file, so each role needs a
// file. A skill file of Codex has no key for the model or the allowed
// tools.
func (a *Agent) writeDefs(ctx context.Context) (def.Set, string, map[string]any, error) {
	if a.cfg.defs == nil {
		return def.Set{}, "", nil, nil
	}
	set, folder, err := a.cfg.defs.Prepare(ctx)
	if err != nil || folder == "" {
		return set, "", nil, err
	}

	var skillRoot string
	if len(set.Skills) > 0 {
		skillRoot = filepath.Join(folder, skillsDir)
		files := slices.Clone(set.Skills)
		for i := range files {
			files[i].Model = ""
			files[i].AllowedTools = nil
		}
		if err := storefs.WriteSkills(skillRoot, files, set.Names); err != nil {
			return def.Set{}, "", nil, err
		}
	}
	roles, err := writeRoles(filepath.Join(folder, filepath.FromSlash(rolesDir)), set.Agents, a.cfg.defs)
	if err != nil {
		return def.Set{}, "", nil, err
	}
	return set, skillRoot, roles, nil
}

// writeRoles writes a role file for each agent to dir, and returns the
// agents table of the config of a thread. A role has no key for the
// Tools, the Disallowed, or the MaxTurns of an agent, so it runs with
// each tool of the session. The role file has no key for the Mode of an
// agent, because Codex 0.163 runs a role with the sandbox of the session
// even if the file sets sandbox_mode. It has no key for the Skills of an
// agent: the CLI lists each skill to the role. It skips a name that
// leaves dir.
func writeRoles(dir string, agents []def.Agent, d *clidefs.Defs) (map[string]any, error) {
	if len(agents) == 0 {
		return nil, nil
	}
	if err := os.MkdirAll(dir, rolesDirPerm); err != nil {
		return nil, fmt.Errorf("write the roles: %w", err)
	}

	roles := make(map[string]any, len(agents))
	for _, a := range agents {
		if !storefs.OneElement(a.Name) {
			continue
		}
		file := filepath.Join(dir, a.Name+roleExt)
		data, err := roleData(a, d.Model(a.Model))
		if err != nil {
			return nil, fmt.Errorf("write the role %s: %w", a.Name, err)
		}
		if err := os.WriteFile(file, data, roleFilePerm); err != nil {
			return nil, fmt.Errorf("write the role %s: %w", a.Name, err)
		}
		roles[a.Name] = map[string]any{
			"description": a.Description,
			"config_file": file,
		}
	}
	return roles, nil
}

// roleData returns the TOML text of the role of a. The CLI fails on a
// role with no instructions, so an agent with no prompt uses its
// description. model is the model ID of the role, or "".
func roleData(a def.Agent, model string) ([]byte, error) {
	instructions := a.Prompt
	if instructions == "" {
		instructions = a.Description
	}
	keys := []struct{ key, value string }{
		{"name", a.Name},
		{"description", a.Description},
		{"developer_instructions", instructions},
		{"model", model},
		{"model_reasoning_effort", effort(a.Effort)},
	}

	var b bytes.Buffer
	for _, k := range keys {
		if k.value == "" {
			continue
		}
		value, err := tomlString(k.value)
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&b, "%s = %s\n", k.key, value)
	}
	return b.Bytes(), nil
}

// tomlString returns s as a TOML basic string. Each escape of a JSON
// string is an escape of a TOML basic string.
func tomlString(s string) (string, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return "", err
	}
	return string(bytes.TrimRight(b.Bytes(), "\n")), nil
}
