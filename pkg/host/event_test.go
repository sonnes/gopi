package host_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/host"
)

var testCall = ai.ToolCall{
	ID:    "call_1",
	Name:  "bash",
	Input: json.RawMessage(`{"command":"ls"}`),
}

func TestEventJSON(t *testing.T) {
	tests := []struct {
		name  string
		event host.Event
		want  string
	}{
		{
			name: "session start",
			event: host.EventSessionStart{
				Session: "s1",
				Model:   "fake/m",
				Mode:    agent.ModePlan,
				Dir:     "/tmp",
				Resumed: true,
			},
			want: `{"type":"session_start","session":"s1","model":"fake/m","mode":"plan","dir":"/tmp","resumed":true}`,
		},
		{
			name:  "session end",
			event: host.EventSessionEnd{Session: "s1", Error: "boom"},
			want:  `{"type":"session_end","session":"s1","error":"boom"}`,
		},
		{
			name: "input",
			event: host.EventInput{
				Session: "s1",
				Entries: host.Text("hi"),
				Policy:  host.Steer,
			},
		},
		{
			name:  "run start",
			event: host.EventRunStart{Session: "s1"},
			want:  `{"type":"run_start","session":"s1"}`,
		},
		{
			name: "run end",
			event: host.EventRunEnd{
				Session: "s1",
				Usage:   ai.Usage{Input: 10, Output: 5},
				Error:   "interrupted",
			},
		},
		{
			name:  "approval start",
			event: host.EventApprovalStart{Session: "s1", Request: agent.ApprovalRequest{Call: testCall}},
			want:  `{"type":"approval_start","session":"s1","call":{"id":"call_1","name":"bash","input":{"command":"ls"}}}`,
		},
		{
			name: "approval start with a reason",
			event: host.EventApprovalStart{
				Session: "s1",
				Request: agent.ApprovalRequest{
					Call:     testCall,
					Reason:   "the command writes",
					NoAlways: true,
				},
			},
			want: `{"type":"approval_start","session":"s1","call":{"id":"call_1","name":"bash","input":{"command":"ls"}},"reason":"the command writes","no_always":true}`,
		},
		{
			name: "approval end",
			event: host.EventApprovalEnd{
				Session:    "s1",
				ToolCallID: "call_1",
				Decision: agent.Decision{
					Allow: true,
					Input: json.RawMessage(`{"command":"ls -l"}`),
				},
			},
			want: `{"type":"approval_end","session":"s1","tool_call_id":"call_1","decision":{"allow":true,"input":{"command":"ls -l"}}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.event)
			require.NoError(t, err)
			if tt.want != "" {
				assert.JSONEq(t, tt.want, string(data))
			}

			got, err := host.UnmarshalEvent(data)
			require.NoError(t, err)
			again, err := json.Marshal(got)
			require.NoError(t, err)
			assert.JSONEq(t, string(data), string(again))
			assert.Equal(t, "s1", got.SessionID())
		})
	}
}

func TestUnmarshalEventUnknownType(t *testing.T) {
	_, err := host.UnmarshalEvent([]byte(`{"type":"nope"}`))
	assert.Error(t, err)
}

func TestStateApply(t *testing.T) {
	start := host.EventSessionStart{Session: "s1", Model: "fake/m"}
	queue := host.EventInput{Session: "s1", Policy: host.Queue}
	steer := host.EventInput{Session: "s1", Policy: host.Steer}
	interrupt := host.EventInput{Session: "s1", Policy: host.Interrupt}
	runStart := host.EventRunStart{Session: "s1"}
	runEnd := host.EventRunEnd{Session: "s1"}
	ask := host.EventApprovalStart{Session: "s1", Request: agent.ApprovalRequest{Call: testCall}}
	answer := host.EventApprovalEnd{Session: "s1", ToolCallID: "call_1"}

	tests := []struct {
		name       string
		events     []host.Event
		wantStatus host.Status
		wantQueued int
		wantWait   int
	}{
		{
			name:       "a new session is idle",
			events:     []host.Event{start},
			wantStatus: host.StatusIdle,
		},
		{
			name:       "a queued message waits",
			events:     []host.Event{start, queue},
			wantStatus: host.StatusIdle,
			wantQueued: 1,
		},
		{
			name:       "a run takes the first message",
			events:     []host.Event{start, queue, interrupt, runStart},
			wantStatus: host.StatusRunning,
			wantQueued: 1,
		},
		{
			name:       "a steered message does not queue",
			events:     []host.Event{start, queue, runStart, steer},
			wantStatus: host.StatusRunning,
		},
		{
			name:       "a run ends",
			events:     []host.Event{start, queue, runStart, runEnd},
			wantStatus: host.StatusIdle,
		},
		{
			name:       "an approval waits",
			events:     []host.Event{start, queue, runStart, ask},
			wantStatus: host.StatusRequiresAction,
			wantWait:   1,
		},
		{
			name:       "an approval ends after its run",
			events:     []host.Event{start, queue, runStart, ask, runEnd, answer},
			wantStatus: host.StatusIdle,
		},
		{
			name:       "an approval ends",
			events:     []host.Event{start, queue, runStart, ask, answer},
			wantStatus: host.StatusRunning,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var s host.State
			for _, e := range tt.events {
				s.Apply(e)
			}
			got, ok := s.Sessions["s1"]
			require.True(t, ok)
			assert.Equal(t, "fake/m", got.Model)
			assert.Equal(t, tt.wantStatus, got.Status())
			assert.Equal(t, tt.wantQueued, got.Queued)
			assert.Len(t, got.Waiting, tt.wantWait)
		})
	}
}

func TestStateApplySessionEnd(t *testing.T) {
	var s host.State
	s.Apply(host.EventSessionStart{Session: "s1"})
	s.Apply(host.EventSessionStart{Session: "s2"})
	s.Apply(host.EventSessionEnd{Session: "s1"})

	assert.NotContains(t, s.Sessions, "s1")
	assert.Contains(t, s.Sessions, "s2")
}

func TestStateClone(t *testing.T) {
	var s host.State
	s.Apply(host.EventSessionStart{Session: "s1"})
	s.Apply(host.EventRunStart{Session: "s1"})
	s.Apply(host.EventApprovalStart{Session: "s1", Request: agent.ApprovalRequest{Call: testCall}})
	s.Apply(host.EventApprovalEnd{Session: "s1", ToolCallID: "call_1"})
	snapshot := s.Clone()

	s.Apply(host.EventApprovalStart{Session: "s1", Request: agent.ApprovalRequest{Call: testCall}})
	s.Apply(host.EventSessionStart{Session: "s2"})

	assert.Empty(t, snapshot.Sessions["s1"].Waiting)
	assert.Equal(t, host.StatusRunning, snapshot.Sessions["s1"].Status())
	assert.NotContains(t, snapshot.Sessions, "s2")
}
