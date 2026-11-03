package host

import (
	"context"
	"fmt"
	"slices"

	"github.com/sonnes/gopi/pkg/def"
	storefs "github.com/sonnes/gopi/pkg/store/fs"
)

// Skills returns the skills of the session id, in the order that the
// model sees them: the skills of [Skillset], then the skills of each
// folder of [Config.Dirs], merged as each driver merges them. A skill
// of a folder replaces a skill of Skillset with the same name.
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
	config, ok, err := configValue.Get(w)
	if err == nil && !ok {
		err = fmt.Errorf("no config: %w", ErrInvalid)
	}
	if err != nil {
		return nil, fmt.Errorf("host: session %s: %w", id, err)
	}
	return h.ConfigSkills(ctx, config)
}

// ConfigSkills returns the skills that a session of config gets, as
// [Host.Skills] does, with no session. It reads only the Dirs of
// config, so a zero config gives the skills of [Skillset].
func (h *Host) ConfigSkills(ctx context.Context, config Config) ([]def.Skill, error) {
	readers := slices.Clone(h.skillset)
	for _, dir := range config.Dirs {
		readers = append(readers, storefs.Dir(dir))
	}
	set, err := def.Resolve(ctx, readers, nil)
	if err != nil {
		return nil, fmt.Errorf("host: the skills: %w", err)
	}
	return set.Skills, nil
}
