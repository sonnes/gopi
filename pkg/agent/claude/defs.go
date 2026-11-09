package claude

import (
	"context"
	"path/filepath"

	"github.com/sonnes/gopi/pkg/agent/internal/clidefs"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/def"
	storefs "github.com/sonnes/gopi/pkg/store/fs"
)

// The folders of the CLI, under a folder of --add-dir. The CLI gives
// these skills and agents their bare names. A folder of --plugin-dir
// adds the name of the folder to each name.
const (
	settingsDir = ".claude"
	skillsDir   = "skills"
	agentsDir   = "agents"
)

// writeDefs loads src and writes its skills and agents in folder f for
// the CLI. It returns the set and the folder for --add-dir. It writes a
// copy of each agent file, because the CLI does not load an agent file
// that is a symbolic link. The tool names of a file become Claude Code
// names. The folder is "" if there is nothing to write.
func writeDefs(ctx context.Context, src def.Source, f *clidefs.Folder) (def.Set, string, error) {
	if f == nil {
		return def.Set{}, "", nil
	}
	set, err := clidefs.Load(ctx, src)
	if err != nil {
		return def.Set{}, "", err
	}
	folder, err := f.Prepare(set)
	if err != nil || folder == "" {
		return set, "", err
	}
	names := set.SkillNames()

	for i := range set.Skills {
		s := &set.Skills[i]
		s.Model = f.Model(s.Model)
		s.AllowedTools = claudeRules(s.AllowedTools)
	}
	for i := range set.Agents {
		a := &set.Agents[i]
		a.Model = f.Model(a.Model)
		if a.Tools != nil {
			a.Tools = claudeRules(a.Tools)
		}
		a.Disallowed = claudeRules(a.Disallowed)
		// An agent file has no value for off.
		if a.Effort == ai.ThinkingOff {
			a.Effort = ""
		}
	}

	settings := filepath.Join(folder, settingsDir)
	skills := filepath.Join(settings, skillsDir)
	if err := storefs.WriteSkills(skills, set.Skills, names); err != nil {
		return def.Set{}, "", err
	}
	agents := filepath.Join(settings, agentsDir)
	if err := storefs.WriteAgents(agents, set.Agents); err != nil {
		return def.Set{}, "", err
	}
	return set, folder, nil
}
