package harness_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/harness"
	"github.com/sonnes/gopi/pkg/session"
)

func TestSubagentGetsNoSections(t *testing.T) {
	ctx := context.Background()
	store := session.NewMemoryStore()
	p := &script{responses: []ai.Message{
		callTool("c1", "agent", `{"agent":"reviewer","prompt":"Review auth.go"}`),
		reply("2 problems"),
		reply("done"),
	}}
	h := harness.New(with(taskOptions(t, p, store, reviewer),
		harness.Sections(func(context.Context) ([]ai.Section, error) {
			return []ai.Section{{Name: "project", Text: "Write tests in Go."}}, nil
		}),
	)...)

	a, err := h.Durable(ctx, "s1")
	require.NoError(t, err)
	run(t, a, "fix it")
	assert.Equal(t, "Write tests in Go.", section(a, "project"))

	child, err := h.Durable(ctx, childID(t, toolResult(t, a)))
	require.NoError(t, err)
	assert.Empty(t, section(child, "project"), "the file of the subagent is its prompt")
}
