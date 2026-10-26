package agent_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/internal/emittest"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
	"github.com/sonnes/gopi/pkg/tool"
)

func TestChildID(t *testing.T) {
	tests := []struct {
		name   string
		parent string
		want   string
	}{
		{
			name:   "a saved parent names the child",
			parent: "s1",
			want:   "s1.reviewer-a1b2",
		},
		{
			name:   "an unsaved parent has an unsaved child",
			parent: "",
			want:   "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, agent.ChildID(tt.parent, "reviewer", "a1b2"))
		})
	}
}

// delegate is a tool that reports one subagent run on the run stream.
var delegate = tool.Func("delegate", func(ctx context.Context, _ ai.ToolCall) (ai.ToolResult, error) {
	agent.EmitSubagent(ctx, agent.EventSubagentStart{
		SessionID: "s1.helper-01",
		Name:      "helper",
		Prompt:    "do it",
	})
	agent.EmitSubagent(ctx, agent.EventSubagent{
		SessionID: "s1.helper-01",
		Event:     agent.EventAgentStart{},
	})
	agent.EmitSubagent(ctx, agent.EventSubagentEnd{
		SessionID: "s1.helper-01",
		Status:    agent.SubagentCompleted,
		Text:      "done",
	})
	return ai.ToolResult{Content: []ai.Content{ai.Text{Text: "done"}}}, nil
})

func TestEmitSubagentInToolCall(t *testing.T) {
	p := &script{responses: []ai.Message{
		toolCall("c1", "delegate", `{}`),
		text("ok"),
	}}
	a := agent.New(model(p), agent.Toolset(delegate))

	events, _, err := collect(t, a.Run(context.Background(), session.Text("hi")))
	require.NoError(t, err)
	emittest.CheckEvents(t, events)

	var subagent []agent.Event
	for _, e := range events {
		switch e.(type) {
		case agent.EventSubagentStart, agent.EventSubagent, agent.EventSubagentEnd:
			subagent = append(subagent, e)
		}
	}
	require.Len(t, subagent, 3)
	assert.Equal(t, "c1", as[agent.EventSubagentStart](t, subagent[0]).ToolCallID, "the call of ctx fills the call ID")
	assert.Equal(t, "c1", as[agent.EventSubagent](t, subagent[1]).ToolCallID)
	assert.Equal(t, "c1", as[agent.EventSubagentEnd](t, subagent[2]).ToolCallID)
}

func TestEmitSubagentOutsideToolCall(t *testing.T) {
	ok := agent.EmitSubagent(context.Background(), agent.EventSubagentStart{Name: "helper"})
	assert.False(t, ok, "no run takes the event")
}

func TestEmitSubagentRejectsOtherEvents(t *testing.T) {
	var ok bool
	probe := tool.Func("probe", func(ctx context.Context, _ ai.ToolCall) (ai.ToolResult, error) {
		ok = agent.EmitSubagent(ctx, agent.EventTurnStart{Turn: 9})
		return ai.ToolResult{}, nil
	})
	p := &script{responses: []ai.Message{
		toolCall("c1", "probe", `{}`),
		text("ok"),
	}}
	a := agent.New(model(p), agent.Toolset(probe))

	events, _, err := collect(t, a.Run(context.Background(), session.Text("hi")))
	require.NoError(t, err)
	emittest.CheckEvents(t, events)
	assert.False(t, ok, "a tool sends only subagent events")
}

func TestParentRoundTrip(t *testing.T) {
	log := session.New()
	ref := agent.ParentRef{
		SessionID:  "s1",
		ToolCallID: "c1",
	}
	require.NoError(t, agent.Parent.Set(context.Background(), log.Writer(), ref))

	got, ok, err := agent.Parent.Get(log)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, ref, got)
}
