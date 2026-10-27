package codex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/internal/emittest"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
)

// subagentEvents splits the subagent events of a run.
func subagentEvents(events []agent.Event) (
	starts []agent.EventSubagentStart,
	ends []agent.EventSubagentEnd,
	inner map[string][]agent.Event,
) {
	inner = map[string][]agent.Event{}
	for _, e := range events {
		switch e := e.(type) {
		case agent.EventSubagentStart:
			starts = append(starts, e)
		case agent.EventSubagentEnd:
			ends = append(ends, e)
		case agent.EventSubagent:
			inner[e.SessionID] = append(inner[e.SessionID], e.Event)
		}
	}
	return starts, ends, inner
}

// TestRecordedSubagent runs the real CLI with one spawn_agent call and
// one wait. Record it with
// `make record-cli AGENT=codex RUN=TestRecordedSubagent`.
func TestRecordedSubagent(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	real, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "word.txt"), []byte("banana\n"), 0o600))
	useTraces(t, []string{"TestRecordedSubagent.trace"},
		replaceScrub(jsonString(real), workDir),
		replaceScrub(jsonString(dir), workDir),
		replaceScrub(jsonString(home), "/home/me"),
		machineScrub,
	)
	store := session.NewMemoryStore()
	a, err := Durable(ctx, "s1",
		Dir(dir),
		Sandbox(SandboxReadOnly),
		Policy(ApprovalNever),
		Thinking(ai.ThinkingLow),
		Store(store),
	)
	require.NoError(t, err)
	t.Cleanup(func() { a.Close() })

	prompt := "Spawn exactly one subagent with spawn_agent. Its task: read the file word.txt " +
		"and reply with the word in it. Wait for the subagent, then reply with that word only."
	events, msgs, err := collect(a.Run(ctx, session.Text(prompt)))
	require.NoError(t, err)
	emittest.CheckEvents(t, events)
	checkLog(t, a)

	starts, ends, inner := subagentEvents(events)
	require.NotEmpty(t, starts, "spawn_agent starts a child")
	start := starts[0]
	assert.Equal(t, "read_word", start.Name, "the name is the last part of the agent path")
	assert.Regexp(t, `^s1\.read_word-\S+$`, start.SessionID)
	assert.Empty(t, start.Prompt, "the server does not send the prompt of a spawn")
	require.NotEmpty(t, ends)
	assert.Equal(t, agent.SubagentCompleted, ends[len(ends)-1].Status)
	assert.Contains(t, strings.ToLower(ends[len(ends)-1].Text), "banana")
	emittest.CheckEvents(t, inner[start.SessionID])

	var names []string
	for _, m := range msgs {
		for _, tc := range m.ToolCalls() {
			names = append(names, tc.Name)
		}
	}
	assert.Contains(t, names, "spawn_agent", "the collab call has the name that the model sees")

	snap, err := store.Load(ctx, start.SessionID)
	require.NoError(t, err)
	parent, ok, err := agent.Parent.Read(snap)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, agent.ParentRef{SessionID: "s1", ToolCallID: start.ToolCallID}, parent)
	assert.NotEmpty(t, snap.Entries)

	require.NotEmpty(t, msgs)
	assert.Contains(t, strings.ToLower(msgs[len(msgs)-1].Text()), "banana")
}

func TestSubagentThatEndsLateGetsAReportTurn(t *testing.T) {
	useTraces(t, []string{"subagent_report.trace"})
	a := New(
		Dir(workDir),
		Sandbox(SandboxReadOnly),
		Policy(ApprovalNever),
		Thinking(ai.ThinkingLow),
	)
	t.Cleanup(func() { a.Close() })

	prompt := "Spawn exactly one subagent with spawn_agent. Its task: read the file word.txt " +
		"and reply with the word in it. Wait for the subagent, then reply with that word only."
	events, msgs, err := collect(a.Run(context.Background(), session.Text(prompt)))
	require.NoError(t, err)
	emittest.CheckEvents(t, events)
	checkLog(t, a)

	_, ends, _ := subagentEvents(events)
	require.Len(t, ends, 1)
	assert.Equal(t, agent.SubagentCompleted, ends[0].Status)

	var report string
	for _, m := range a.Log().Messages() {
		if m.Role == ai.RoleUser {
			report = m.Text()
		}
	}
	assert.Equal(t, "Subagent completed: read_word\n\nResult:\nbanana", report,
		"the run gives the model the result that it did not see")
	assert.Equal(t, "The subagent found banana.", msgs[len(msgs)-1].Text())
}

func TestCollabToolName(t *testing.T) {
	tests := []struct {
		tool string
		want string
	}{
		{tool: "wait", want: "wait"},
		{tool: "spawnAgent", want: "spawn_agent"},
		{tool: "sendInput", want: "send_input"},
		{tool: "closeAgent", want: "close_agent"},
	}
	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			assert.Equal(t, tt.want, collabToolName(tt.tool))
		})
	}
}

func TestCancelInterruptsTheChild(t *testing.T) {
	useTraces(t, []string{"subagent_cancel.trace"})
	a := New(
		Dir(workDir),
		Sandbox(SandboxReadOnly),
		Policy(ApprovalNever),
		Thinking(ai.ThinkingLow),
	)
	t.Cleanup(func() { a.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	prompt := "Spawn exactly one subagent with spawn_agent. Its task: read the file word.txt " +
		"and reply with the word in it. Wait for the subagent, then reply with that word only."
	s := a.Run(ctx, session.Text(prompt))
	var (
		events []agent.Event
		runErr error
	)
	for ev, err := range s.Events() {
		if err != nil {
			runErr = err
			break
		}
		events = append(events, ev)
		sub, ok := ev.(agent.EventSubagent)
		if !ok {
			continue
		}
		if _, ok := sub.Event.(agent.EventToolExecutionStart); ok {
			cancel()
		}
	}
	require.ErrorIs(t, runErr, context.Canceled)
	emittest.CheckEvents(t, events)
	checkLog(t, a)

	_, ends, inner := subagentEvents(events)
	require.Len(t, ends, 1)
	assert.Equal(t, agent.SubagentInterrupted, ends[0].Status, "the cancel stops the child too")
	emittest.CheckEvents(t, inner[ends[0].SessionID])
}

// subagentPrompt is the prompt of the recorded subagent traces.
const subagentPrompt = "Spawn exactly one subagent with spawn_agent. Its task: read the file word.txt " +
	"and reply with the word in it. Wait for the subagent, then reply with that word only."

// newSubagentAgent returns the agent of the hand-written subagent traces.
func newSubagentAgent(t *testing.T, trace string) *Agent {
	t.Helper()
	useTraces(t, []string{trace})
	a := New(
		Dir(workDir),
		Sandbox(SandboxReadOnly),
		Policy(ApprovalNever),
		Thinking(ai.ThinkingLow),
	)
	t.Cleanup(func() { a.Close() })
	return a
}

func TestChildThatFailsFailsTheRun(t *testing.T) {
	a := newSubagentAgent(t, "subagent_bad_item.trace")

	events, _, err := collect(a.Run(context.Background(), session.Text(subagentPrompt)))
	require.ErrorContains(t, err, "item/completed", "an error of a child is an error of the run")
	emittest.CheckEvents(t, events)
	_, ends, _ := subagentEvents(events)
	require.Len(t, ends, 1)
	assert.Equal(t, agent.SubagentInterrupted, ends[0].Status)
}

func TestFailedTurnInterruptsItsChildren(t *testing.T) {
	a := newSubagentAgent(t, "subagent_root_failed.trace")

	events, _, err := collect(a.Run(context.Background(), session.Text(subagentPrompt)))
	require.Error(t, err)
	emittest.CheckEvents(t, events)
	_, ends, _ := subagentEvents(events)
	require.Len(t, ends, 1)
	assert.Equal(t, agent.SubagentInterrupted, ends[0].Status, "the trace has the turn/interrupt of the child")
	assert.Nil(t, a.conn, "a child can still run on the server, so the next run starts a new process")
}

func TestCancelBeforeTheChildTurnStartsInterruptsIt(t *testing.T) {
	a := newSubagentAgent(t, "subagent_cancel_early.trace")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := a.Run(ctx, session.Text(subagentPrompt))
	var events []agent.Event
	start := time.Now()
	for ev, err := range s.Events() {
		if err != nil {
			break
		}
		events = append(events, ev)
		if _, ok := ev.(agent.EventSubagentStart); ok {
			cancel()
		}
	}
	_, err := s.Wait()
	require.ErrorIs(t, err, context.Canceled)
	emittest.CheckEvents(t, events)
	assert.Less(t, time.Since(start), interruptTimeout, "the child that starts after the cancel stops at once")
}

func TestReportTurnTakesNotificationsBeforeItsResponse(t *testing.T) {
	a := newSubagentAgent(t, "subagent_report_early.trace")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	events, msgs, err := collect(a.Run(ctx, session.Text(subagentPrompt)))
	require.NoError(t, err)
	emittest.CheckEvents(t, events)
	assert.Equal(t, "The subagent found banana.", msgs[len(msgs)-1].Text())
}
