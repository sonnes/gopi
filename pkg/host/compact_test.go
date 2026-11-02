package host_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/host"
)

func TestCompactRunsInARunOfItsOwn(t *testing.T) {
	f := newFixture(t, host.Compaction(noCompactor{}))
	f.create(t, host.Config{ID: "s1"})
	_, events := f.watch(t)
	f.send(t, "s1", "a")
	assert.Equal(t, host.StopEndTurn, runEnd(t, events).StopReason)

	_, agentEvents := f.follow(t, "s1")
	require.NoError(t, f.h.Compact(context.Background(), "s1", "keep it"))

	assert.Equal(t, host.StopEndTurn, runEnd(t, events).StopReason)
	var started bool
	for ev := range agentEvents {
		if _, ok := ev.(agent.EventCompactionStart); ok {
			started = true
		}
		if _, ok := ev.(agent.EventAgentEnd); ok {
			break
		}
	}
	assert.True(t, started)
}

func TestCompactFailures(t *testing.T) {
	ctx := context.Background()

	t.Run("a session with no message", func(t *testing.T) {
		f := newFixture(t, host.Compaction(noCompactor{}))
		f.create(t, host.Config{ID: "s1"})
		assert.ErrorIs(t, f.h.Compact(ctx, "s1", ""), host.ErrInvalid)
	})

	t.Run("an active run", func(t *testing.T) {
		f, gate := gated(t)
		f.create(t, host.Config{ID: "s1"})
		f.send(t, "s1", "a")
		f.model.waitCalls(t, 1)
		assert.ErrorIs(t, f.h.Compact(ctx, "s1", ""), host.ErrBusy)
		close(gate)
	})

	t.Run("an agent that cannot compact", func(t *testing.T) {
		f := newFixture(t)
		f.driver.plain = true
		f.create(t, host.Config{ID: "s1"})
		assert.ErrorIs(t, f.h.Compact(ctx, "s1", ""), errors.ErrUnsupported)
	})
}

func TestAutoCompactReachesEachDriverStart(t *testing.T) {
	f := newFixture(t)

	f.create(t, host.Config{ID: "s1", AutoCompact: 300_000})

	assert.Equal(t, 300_000, f.lastConfig(t).AutoCompact)
}
