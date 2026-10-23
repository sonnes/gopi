package def_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/def"
)

func TestAnAgentIsAReader(t *testing.T) {
	a := def.Agent{
		Name:        "reviewer",
		Description: "Review a diff.",
		Prompt:      "Read the diff.",
	}

	got, err := a.ReadAgents(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []def.Agent{a}, got)
}
