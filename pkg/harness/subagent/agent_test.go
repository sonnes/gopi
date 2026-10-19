package subagent_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/harness/subagent"
)

func TestAnAgentIsAReader(t *testing.T) {
	a := subagent.Agent{
		Name:        "reviewer",
		Description: "Review a diff.",
		Prompt:      "Read the diff.",
	}

	got, err := a.ReadAgents(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []subagent.Agent{a}, got)
}
