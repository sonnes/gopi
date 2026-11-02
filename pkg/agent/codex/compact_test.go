package codex

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/internal/emittest"
	"github.com/sonnes/gopi/pkg/session"
)

// compactions returns the compaction entries of the log of a.
func compactions(t *testing.T, a *Agent) []session.Compaction {
	t.Helper()
	var out []session.Compaction
	for _, e := range a.Log().Entries() {
		if e.Kind != session.KindCompaction {
			continue
		}
		var c session.Compaction
		require.NoError(t, json.Unmarshal(e.Data, &c))
		out = append(out, c)
	}
	return out
}

func TestCompactOnTheCLI(t *testing.T) {
	ctx := context.Background()
	useTraces(t, []string{"compact.trace"})
	a := newAgent(t)

	events, msgs, err := collect(a.Run(ctx, session.Text("Hi.")))
	require.NoError(t, err)
	emittest.CheckEvents(t, events)
	require.Len(t, msgs, 1)
	assert.Equal(t, "Hello", msgs[0].Text(), "a compaction does not break the message after it")

	events, msgs, err = collect(a.Compact(ctx, "the CLI does not take this"))
	require.NoError(t, err)
	emittest.CheckEvents(t, events)
	assert.Empty(t, msgs)

	var starts, ends int
	for _, ev := range events {
		switch ev.(type) {
		case agent.EventCompactionStart:
			starts++
		case agent.EventCompaction:
			ends++
		}
	}
	assert.Equal(t, 1, starts)
	assert.Equal(t, 1, ends)

	got := compactions(t, a)
	require.Len(t, got, 2)
	assert.Equal(t, session.TriggerAuto, got[0].Trigger)
	assert.Equal(t, session.TriggerManual, got[1].Trigger)
	assert.Equal(t, strategyName, got[1].Strategy)
}

func TestCompactThatFails(t *testing.T) {
	useTraces(t, []string{"compact_failed.trace"})
	a := newAgent(t)

	events, _, err := collect(a.Compact(context.Background(), ""))
	require.ErrorContains(t, err, "the compaction failed")

	var failure string
	for _, ev := range events {
		if v, ok := ev.(agent.EventCompaction); ok {
			failure = v.Error
		}
	}
	assert.Contains(t, failure, "the compaction failed")
	assert.Empty(t, compactions(t, a))
}

func TestAutoCompactGoesInTheThreadConfig(t *testing.T) {
	a := newAgent(t, AutoCompact(300000))

	p := a.threadParams("", nil, "", nil)

	assert.Equal(t, 300000, p.Config["model_auto_compact_token_limit"])
}
