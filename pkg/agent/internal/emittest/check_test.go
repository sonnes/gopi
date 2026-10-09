package emittest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/internal/emittest"
	"github.com/sonnes/gopi/pkg/ai"
)

// failer is a [testing.TB] that records a failure and does not fail
// the test that runs it.
type failer struct {
	testing.TB
	failed bool
}

func (f *failer) Helper() {}

func (f *failer) Errorf(string, ...any) { f.failed = true }

func TestCheckEventsFails(t *testing.T) {
	tests := []struct {
		name   string
		events []agent.Event
	}{
		{
			name:   "no events",
			events: nil,
		},
		{
			name: "open turn",
			events: []agent.Event{
				agent.EventAgentStart{},
				agent.EventTurnStart{},
			},
		},
		{
			name: "update without a model event",
			events: []agent.Event{
				agent.EventAgentStart{},
				agent.EventTurnStart{},
				agent.EventMessageStart{},
				agent.EventMessageUpdate{},
				agent.EventMessageEnd{},
				agent.EventTurnEnd{},
			},
		},
		{
			name: "assistant message outside a turn",
			events: []agent.Event{
				agent.EventAgentStart{},
				agent.EventMessageStart{Message: ai.Message{Role: ai.RoleAssistant}},
				agent.EventMessageEnd{Message: ai.Message{Role: ai.RoleAssistant}},
			},
		},
		{
			name: "no agent_end",
			events: []agent.Event{
				agent.EventAgentStart{},
				agent.EventTurnStart{},
				agent.EventTurnEnd{},
			},
		},
		{
			name: "agent_end not last",
			events: []agent.Event{
				agent.EventAgentStart{},
				agent.EventAgentEnd{},
				agent.EventTurnStart{},
				agent.EventTurnEnd{},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &failer{TB: t}
			emittest.CheckEvents(fake, tt.events)
			assert.True(t, fake.failed)
		})
	}
}

func TestCheckEventsUserMessageBetweenTurns(t *testing.T) {
	user := ai.Message{Role: ai.RoleUser}
	fake := &failer{TB: t}
	emittest.CheckEvents(fake, []agent.Event{
		agent.EventAgentStart{},
		agent.EventMessageStart{Message: user},
		agent.EventMessageEnd{Message: user},
		agent.EventTurnStart{},
		agent.EventTurnEnd{},
		agent.EventAgentEnd{},
	})
	assert.False(t, fake.failed)
}
