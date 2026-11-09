package host_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/def"
	"github.com/sonnes/gopi/pkg/host"
)

// writeSkill writes the skill name with the description desc in
// dir/skills.
func writeSkill(t *testing.T, dir, name, desc string) {
	t.Helper()
	folder := filepath.Join(dir, "skills", name)
	require.NoError(t, os.MkdirAll(folder, 0o755))
	text := "---\ndescription: " + desc + "\n---\nDo it.\n"
	require.NoError(t, os.WriteFile(filepath.Join(folder, "SKILL.md"), []byte(text), 0o644))
}

// descriptions returns the name and the description of each skill.
func descriptions(skills []def.Skill) [][2]string {
	out := make([][2]string, len(skills))
	for i, s := range skills {
		out[i] = [2]string{s.Name, s.Description}
	}
	return out
}

func TestSkillsReplaceASkillOfTheHostWithAFolderSkill(t *testing.T) {
	f := newFixture(t, host.Defs(def.Set{Skills: []def.Skill{
		{Name: "deploy", Description: "Code.", Body: "b"},
		{Name: "lw-tasks", Description: "Tasks.", Body: "b"},
	}}))
	low := t.TempDir()
	high := t.TempDir()
	writeSkill(t, low, "deploy", "Low.")
	writeSkill(t, low, "review", "Review.")
	writeSkill(t, high, "deploy", "High.")
	f.create(t, host.Config{
		ID:      "s1",
		DefDirs: []string{low, high},
	})

	skills, err := f.h.Skills(context.Background(), "s1")
	require.NoError(t, err)

	assert.Equal(t, [][2]string{
		{"deploy", "High."},
		{"lw-tasks", "Tasks."},
		{"review", "Review."},
	}, descriptions(skills), "a later folder wins at the place of the skill of the host")
}

func TestConfigSkillsWithNoDefDirsGivesTheSkillsOfTheHost(t *testing.T) {
	inline := def.Skill{Name: "deploy", Description: "D.", Body: "b"}
	f := newFixture(t, host.Defs(def.Set{Skills: []def.Skill{inline}}))

	skills, err := f.h.ConfigSkills(context.Background(), host.Config{})
	require.NoError(t, err)

	assert.Equal(t, []def.Skill{inline}, skills)
}

func TestSkillsOfAnUnknownSessionFails(t *testing.T) {
	f := newFixture(t)

	_, err := f.h.Skills(context.Background(), "missing")

	assert.ErrorIs(t, err, host.ErrNotFound)
}
