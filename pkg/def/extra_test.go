package def_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/def"
)

func TestExtraReadsAMapAsAStruct(t *testing.T) {
	type meta struct {
		Owner   string `json:"owner"`
		Version string `json:"version"`
	}
	e := def.Extra{
		"metadata": map[string]any{
			"owner":   "platform",
			"version": "3",
		},
	}

	got, ok, err := e.Read[meta]("metadata")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, meta{Owner: "platform", Version: "3"}, got)
}

func TestExtraReadsAScalar(t *testing.T) {
	e := def.Extra{"color": "blue"}

	got, ok, err := e.Read[string]("color")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "blue", got)
}

func TestExtraWithoutTheKeyGivesTheZeroValue(t *testing.T) {
	tests := []struct {
		name  string
		extra def.Extra
	}{
		{"a nil map", nil},
		{"another key", def.Extra{"color": "blue"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok, err := tt.extra.Read[[]string]("tags")
			require.NoError(t, err)
			assert.False(t, ok)
			assert.Nil(t, got)
		})
	}
}

func TestExtraFailsOnAValueOfAnotherType(t *testing.T) {
	e := def.Extra{"color": "blue"}

	_, ok, err := e.Read[int]("color")
	assert.True(t, ok)
	assert.ErrorContains(t, err, `def: extra key "color"`)
}
