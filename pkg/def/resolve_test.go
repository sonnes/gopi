package def_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/def"
)

// fakeDir is a [def.Reader] of definitions in code.
type fakeDir struct {
	agents []def.Agent
	skills []def.Skill
	err    error
}

func (d fakeDir) ReadAgents(context.Context) ([]def.Agent, error) { return d.agents, d.err }
func (d fakeDir) ReadSkills(context.Context) ([]def.Skill, error) { return d.skills, d.err }

func TestResolve_LaterDirWinsAtTheEarlierPosition(t *testing.T) {
	low := fakeDir{
		agents: []def.Agent{{Name: "a", Prompt: "low"}, {Name: "b", Prompt: "low"}},
		skills: []def.Skill{{Name: "x", Body: "low"}, {Name: "y", Body: "low"}},
	}
	high := fakeDir{
		agents: []def.Agent{{Name: "a", Prompt: "high"}},
		skills: []def.Skill{{Name: "x", Body: "high"}},
	}

	set, err := def.Resolve(t.Context(), []def.SkillsReader{low, high}, []def.AgentsReader{low, high})

	require.NoError(t, err)
	assert.Equal(t, []def.Agent{{Name: "a", Prompt: "high"}, {Name: "b", Prompt: "low"}}, set.Agents)
	assert.Equal(t, []def.Skill{{Name: "x", Body: "high"}, {Name: "y", Body: "low"}}, set.Skills)
	assert.Equal(t, []string{"x", "y"}, set.Names)
}

func TestResolve_LastSkillWithABareNameTakesIt(t *testing.T) {
	dir := fakeDir{
		skills: []def.Skill{
			{Name: "deploy", Qualifier: "ops"},
			{Name: "deploy"},
		},
	}

	set, err := def.Resolve(t.Context(), []def.SkillsReader{dir}, nil)

	require.NoError(t, err)
	assert.Equal(t, []string{"ops:deploy", "deploy"}, set.Names)
}

func TestResolve_ReaderErrorFails(t *testing.T) {
	boom := errors.New("boom")

	_, err := def.Resolve(t.Context(), []def.SkillsReader{fakeDir{err: boom}}, nil)

	assert.ErrorIs(t, err, boom)
}

func TestSet_Find(t *testing.T) {
	set, err := def.Resolve(t.Context(), []def.SkillsReader{fakeDir{
		skills: []def.Skill{{Name: "deploy", Qualifier: "ops"}, {Name: "deploy"}},
	}}, nil)
	require.NoError(t, err)

	bare, ok := set.Find("deploy")
	assert.True(t, ok)
	assert.Empty(t, bare.Qualifier)

	qualified, ok := set.Find("ops:deploy")
	assert.True(t, ok)
	assert.Equal(t, "ops", qualified.Qualifier)

	_, ok = set.Find("missing")
	assert.False(t, ok)
}
