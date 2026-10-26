package host_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/host"
)

// waitingSession is a session with a call that waits.
var waitingSession = host.Session{
	ID:     "s1",
	Status: host.StatusRequiresAction,
	Waiting: []agent.ApprovalRequest{{
		Call: ai.ToolCall{
			ID:    "call_1",
			Name:  "Bash",
			Input: json.RawMessage(`{"command":"mkdir x"}`),
		},
		Reason:   "the command writes",
		NoAlways: true,
	}},
	Model:     "fake/m",
	Mode:      agent.ModeDefault,
	UpdatedAt: time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC),
}

func TestEventJSON(t *testing.T) {
	e := host.Event{Session: waitingSession}

	data, err := json.Marshal(e)
	require.NoError(t, err)
	var got host.Event
	require.NoError(t, json.Unmarshal(data, &got))

	assert.Equal(t, e, got)
	assert.Contains(t, string(data), `"status":"requires_action"`)
}

func TestStateApply(t *testing.T) {
	var s host.State

	s.Apply(host.Event{Session: waitingSession})
	idle := waitingSession
	idle.Status = host.StatusIdle
	idle.StopReason = host.StopEndTurn
	idle.Waiting = nil
	s.Apply(host.Event{Session: idle})

	assert.Equal(t, map[string]host.Session{"s1": idle}, s.Sessions, "each event sets the whole session")
}

func TestStateClone(t *testing.T) {
	var s host.State
	s.Apply(host.Event{Session: waitingSession})

	c := s.Clone()
	c.Sessions["s1"].Waiting[0].Reason = "changed"
	c.Sessions["s2"] = host.Session{ID: "s2"}

	assert.Equal(t, "the command writes", s.Sessions["s1"].Waiting[0].Reason)
	assert.NotContains(t, s.Sessions, "s2")
	assert.Equal(t, host.State{}, host.State{}.Clone())
}
