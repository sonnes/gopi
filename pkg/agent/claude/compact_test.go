package claude_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/claude"
	"github.com/sonnes/gopi/pkg/agent/internal/emittest"
	"github.com/sonnes/gopi/pkg/session"
)

// collectCompact drains the stream of a compaction.
func collectCompact(t *testing.T, s *agent.Stream) ([]agent.Event, error) {
	t.Helper()
	var events []agent.Event
	for ev, err := range s.Events() {
		if err != nil {
			break
		}
		events = append(events, ev)
	}
	_, err := s.Wait()
	return events, err
}

func TestCompactStoresTheCompactionAndNotTheCommand(t *testing.T) {
	claude.Replay(t, nil, "testdata/compact.trace")
	a := claude.New(agent.Config{})
	defer closeAgent(t, a)
	ctx := context.Background()

	_, _, err := run(ctx, a, "Hi")
	require.NoError(t, err)

	events, err := collectCompact(t, a.Compact(ctx, "keep the schema"))
	require.NoError(t, err)
	emittest.CheckEvents(t, events)

	var started bool
	var ended *agent.EventCompaction
	for _, ev := range events {
		switch v := ev.(type) {
		case agent.EventCompactionStart:
			started = true
		case agent.EventCompaction:
			ended = &v
		}
	}
	assert.True(t, started)
	require.NotNil(t, ended)
	assert.Empty(t, ended.Error)

	var stored session.Compaction
	var messages int
	for _, e := range a.Log().Entries() {
		switch e.Kind {
		case "":
			messages++
		case session.KindCompaction:
			require.NoError(t, json.Unmarshal(e.Data, &stored))
		}
	}
	assert.Equal(t, 2, messages, "the log keeps no /compact and no summary as a message")
	assert.Equal(t, session.TriggerManual, stored.Trigger)
	assert.Equal(t, 29183, stored.Before)
	assert.Equal(t, 10893, stored.After)
	assert.Contains(t, stored.Summary, "The schema.")
	assert.Equal(t, 2, a.Log().History().CompactedAt)
	assert.Empty(t, stored.Edits, "a CLI keeps its own session")
	checkLog(t, a)
}

func TestCompactThatFails(t *testing.T) {
	claude.Replay(t, nil, "testdata/compact_failed.trace")
	a := claude.New(agent.Config{})
	defer closeAgent(t, a)

	events, err := collectCompact(t, a.Compact(context.Background(), ""))
	require.NoError(t, err)

	var failure string
	for _, ev := range events {
		if v, ok := ev.(agent.EventCompaction); ok {
			failure = v.Error
		}
	}
	assert.Equal(t, "the context is too small", failure)
	assert.Empty(t, a.Log().Entries())
}

func TestAutoCompactGoesToTheCLI(t *testing.T) {
	args := filepath.Join(t.TempDir(), "args")
	body := "echo \"$@\" > " + args + "\nexit 1"
	a := claude.New(agent.Config{AutoCompact: 300000}, claude.Binary(fakeCLI(t, body)))
	defer a.Close()

	_, _, err := run(context.Background(), a, "Hi")
	require.Error(t, err)

	got, err := os.ReadFile(args)
	require.NoError(t, err)
	assert.Contains(t, string(got), "--autocompact 300000")
}
