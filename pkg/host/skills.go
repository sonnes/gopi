package host

import (
	"context"
	"fmt"

	"github.com/sonnes/gopi/pkg/def"
	storefs "github.com/sonnes/gopi/pkg/store/fs"
)

// Skills returns the skills of the session id, in the order that the
// model sees them: the skills of [Defs], then the skills of each folder
// of [Config.DefDirs], merged with [def.Layers] as each driver merges
// them. A skill of a folder replaces a skill of Defs with the same name.
//
// A user message that starts with "/" and the qualified name of a
// skill, such as "/deploy now", loads the skill. Each driver reads the
// text, so a caller sends the text as it is. The user can load a skill
// with [def.Skill.NoModelCall] too.
//
// Skills reads the folders at each call, so an edit of a folder shows
// at once. The agent of the session gets the edit at its next start. If
// the host does not know the session, Skills returns [ErrNotFound]. The
// session of a subagent has no config, so Skills returns [ErrInvalid].
func (h *Host) Skills(ctx context.Context, id string) ([]def.Skill, error) {
	s, err := h.session(id)
	if err != nil {
		return nil, err
	}
	w, err := s.log(ctx)
	if err != nil {
		return nil, err
	}
	config, err := s.storedConfig(configValue.Get(w))
	if err != nil {
		return nil, err
	}
	return h.ConfigSkills(ctx, config)
}

// ConfigSkills returns the skills that a session of config gets, as
// [Host.Skills] does, with no session. It reads only the DefDirs of
// config, so a zero config gives the skills of [Defs].
func (h *Host) ConfigSkills(ctx context.Context, config Config) ([]def.Skill, error) {
	set, err := h.configDefs(config).Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("host: the skills: %w", err)
	}
	return set.Skills, nil
}

// configDefs returns the definitions of a session of config: the source
// of [Defs], then each folder of config.DefDirs.
func (h *Host) configDefs(config Config) def.Source {
	sources := []def.Source{h.defs}
	for _, dir := range config.DefDirs {
		sources = append(sources, storefs.Dir(dir))
	}
	return def.Layers(sources...)
}
