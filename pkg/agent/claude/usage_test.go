package claude_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/claude"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
)

// runEnd returns the agent_end of the events of one run.
func runEnd(t *testing.T, events []agent.Event) agent.EventAgentEnd {
	t.Helper()
	require.NotEmpty(t, events)
	end, ok := events[len(events)-1].(agent.EventAgentEnd)
	require.True(t, ok, "agent_end is last")
	return end
}

// checkByModel checks the tokens of each model, and that the parts of
// each cost sum to the reported cost.
func checkByModel(t *testing.T, end agent.EventAgentEnd, want map[string]ai.Usage, cost map[string]float64) {
	t.Helper()
	require.Len(t, end.UsageByModel, len(want))
	var sum ai.Usage
	for model, w := range want {
		got := end.UsageByModel[model]
		assert.InDelta(t, cost[model], got.Cost.Total(), 1e-12, model)
		got.Cost = ai.Cost{}
		assert.Equal(t, w, got, model)
		sum = sum.Add(end.UsageByModel[model])
	}
	assert.Equal(t, sum, end.Usage, "usage is the sum of the models")
}

func TestUsageByModelIsTheDifferenceOfEachRun(t *testing.T) {
	claude.Replay(t, nil, "testdata/model_usage.trace")
	a := claude.New()
	defer closeAgent(t, a)

	events, _, err := run(context.Background(), a, "Say hello")
	require.NoError(t, err)
	checkByModel(t, runEnd(t, events),
		map[string]ai.Usage{"claude-sonnet-5-5": {Input: 100, Output: 10, CacheRead: 200, CacheWrite: 300}},
		map[string]float64{"claude-sonnet-5-5": 0.02})

	events, _, err = run(context.Background(), a, "Again")
	require.NoError(t, err)
	checkByModel(t, runEnd(t, events),
		map[string]ai.Usage{
			"claude-sonnet-5-5": {Input: 50, Output: 10, CacheRead: 200},
			"claude-haiku-5-5":  {Input: 5, Output: 1},
		},
		map[string]float64{"claude-sonnet-5-5": 0.01, "claude-haiku-5-5": 0.001})
}

func TestUsageByModelOfAResumedSession(t *testing.T) {
	ctx := context.Background()
	store := session.NewMemoryStore()
	claude.Replay(t, nil, "testdata/model_usage_first.trace", "testdata/model_usage_resume.trace")

	first, err := claude.Durable(ctx, "s1", claude.Store(store))
	require.NoError(t, err)
	_, _, err = run(ctx, first, "Say hello")
	require.NoError(t, err)
	require.NoError(t, first.Close())

	second, err := claude.Durable(ctx, "s1", claude.Store(store))
	require.NoError(t, err)
	defer closeAgent(t, second)
	events, _, err := run(ctx, second, "Again")
	require.NoError(t, err)
	checkByModel(t, runEnd(t, events),
		map[string]ai.Usage{
			"claude-sonnet-5-5": {Input: 50, Output: 10, CacheRead: 200},
			"claude-haiku-5-5":  {Input: 5, Output: 1},
		},
		map[string]float64{"claude-sonnet-5-5": 0.01, "claude-haiku-5-5": 0.001})
}
