package harness

import (
	"context"
	"slices"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/def"
)

// slashSkills returns the hook that loads the skill that a user message
// calls, as [def.Set.Slash] reads the first text of the message. The
// request gets the body of the skill before that text, as the skill
// tool gives it. A user can load a skill that the model cannot call.
//
// The log keeps the message as the user wrote it, and the hook adds the
// body at each model call. The body of each call is the same, so the
// start of the request stays cached. An edit of the skill reaches the
// next assembly.
func (a *Assembly) slashSkills() agent.Hook {
	set := a.defs
	return agent.BeforeCall(func(_ context.Context, _ agent.State, r agent.Request) (agent.Request, error) {
		var msgs []ai.Message
		for i, m := range r.Messages {
			loaded, ok := withSkillBody(m, set)
			if !ok {
				continue
			}
			if msgs == nil {
				msgs = slices.Clone(r.Messages)
			}
			msgs[i] = loaded
		}
		if msgs != nil {
			r.Messages = msgs
		}
		return r, nil
	})
}

// withSkillBody returns a copy of m with the body of the skill that its
// first text calls before that text, and reports whether m calls a
// skill of set.
func withSkillBody(m ai.Message, set def.Set) (ai.Message, bool) {
	if m.Role != ai.RoleUser {
		return m, false
	}
	at := slices.IndexFunc(m.Content, func(c ai.Content) bool {
		_, ok := c.(ai.Text)
		return ok
	})
	if at == -1 {
		return m, false
	}
	name, _, ok := set.Slash(m.Content[at].(ai.Text).Text)
	if !ok {
		return m, false
	}
	s, _ := set.Skill(name)
	body := ai.Text{Text: skillHeading(name) + skillBody(s)}
	m.Content = slices.Insert(slices.Clone(m.Content), at, ai.Content(body))
	return m, true
}
