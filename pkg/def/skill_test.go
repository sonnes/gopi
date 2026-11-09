package def_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sonnes/gopi/pkg/def"
)

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
			s := def.Skill{
				Name:      "deploy",
				Qualifier: tt.qualifier,
			}

			assert.Equal(t, tt.want, s.QualifiedName())
		})
	}
}
