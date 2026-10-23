package def_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/def"
)

// failingAgents is an agents reader that always fails.
type failingAgents struct{}

func (failingAgents) ReadAgents(context.Context) ([]def.Agent, error) {
	return nil, errors.New("disk on fire")
}

func TestFindAgentGivesTheLastAgentWithTheName(t *testing.T) {
	first := def.Agent{Name: "reviewer", Prompt: "first"}
	other := def.Agent{Name: "writer", Prompt: "other"}
	last := def.Agent{Name: "reviewer", Prompt: "last"}

	got, err := def.FindAgent(
		context.Background(),
		"reviewer",
		first,
		other,
		last,
	)
	require.NoError(t, err)
	assert.Equal(t, last, got)
}

func TestFindAgentWithoutTheNameIsNotFound(t *testing.T) {
	_, err := def.FindAgent(
		context.Background(),
		"reviewer",
		def.Agent{Name: "writer"},
	)
	assert.ErrorIs(t, err, def.ErrNotFound)
}

func TestFindAgentFailsOnAReaderError(t *testing.T) {
	_, err := def.FindAgent(
		context.Background(),
		"reviewer",
		def.Agent{Name: "reviewer"},
		failingAgents{},
	)
	assert.ErrorContains(t, err, "disk on fire")
}
