package skill_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/harness/skill"
)

func TestASkillIsAReader(t *testing.T) {
	s := skill.Skill{
		Name:        "deploy",
		Description: "Deploy.",
		Body:        "Run it.",
	}

	got, err := s.ReadSkills(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []skill.Skill{s}, got)
}

func TestQualifiedName(t *testing.T) {
	tests := []struct {
		name      string
		qualifier string
		want      string
	}{
		{"no qualifier", "", "deploy"},
		{"a relative path", "apps/web", "apps/web:deploy"},
		{"the home folder", "~", "~:deploy"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := skill.Skill{
				Name:      "deploy",
				Qualifier: tt.qualifier,
			}

			assert.Equal(t, tt.want, s.QualifiedName())
		})
	}
}
