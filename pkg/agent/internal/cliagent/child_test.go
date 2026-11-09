package cliagent_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/internal/cliagent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
)

func TestOpenChildWritesTheParentOnce(t *testing.T) {
	ctx := context.Background()
	store := session.NewMemoryStore()
	core, err := cliagent.Open(ctx, "test", agent.Config{
		SessionID: "s1",
		Store:     store,
	})
	require.NoError(t, err)
	drop := func(agent.Event) {}

	child, err := core.OpenChild(ctx, "s1", "helper", "01", "c1", drop)
	require.NoError(t, err)
	entries := len(child.Log.Entries())
	again, err := core.OpenChild(ctx, "s1", "helper", "01", "c1", drop)
	require.NoError(t, err)

	assert.Equal(t, child.SessionID, again.SessionID)
	assert.Len(t, again.Log.Entries(), entries, "a child that exists keeps its entries")
	snap, err := store.Load(ctx, child.SessionID)
	require.NoError(t, err)
	got, ok, err := agent.Parent.Read(snap)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, agent.ParentRef{SessionID: "s1", ToolCallID: "c1"}, got)
}

func TestOpenChildOfAnUnsavedParentIsUnsaved(t *testing.T) {
	ctx := context.Background()
	c := agent.Config{}
	core := cliagent.New("test", &c)

	child, err := core.OpenChild(ctx, core.SessionID(), "helper", "01", "c1", func(agent.Event) {})
	require.NoError(t, err)

	assert.Empty(t, child.SessionID)
	require.NotNil(t, child.Log)
}

func TestOpenChildSendsSubagentEvents(t *testing.T) {
	ctx := context.Background()
	c := agent.Config{}
	core := cliagent.New("test", &c)
	var got []agent.Event

	child, err := core.OpenChild(ctx, "", "helper", "01", "c1", func(ev agent.Event) {
		got = append(got, ev)
	})
	require.NoError(t, err)
	require.NoError(t, child.Emitter.Start())

	assert.Equal(t, []agent.Event{
		agent.EventSubagent{
			ToolCallID: "c1",
			Event:      agent.EventAgentStart{},
		},
	}, got)
}

func TestEndText(t *testing.T) {
	msgs := []ai.Message{{
		Role:    ai.RoleAssistant,
		Content: []ai.Content{ai.Text{Text: "done"}},
	}}
	cause := errors.New("the CLI exited")

	tests := []struct {
		name   string
		status string
		err    error
		want   string
	}{
		{
			name:   "completed",
			status: agent.SubagentCompleted,
			want:   "done",
		},
		{
			name:   "failed with an error",
			status: agent.SubagentFailed,
			err:    cause,
			want:   "the CLI exited",
		},
		{
			name:   "interrupted with an error",
			status: agent.SubagentInterrupted,
			err:    cause,
			want:   "done",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, cliagent.EndText(msgs, tt.status, tt.err))
		})
	}
}
