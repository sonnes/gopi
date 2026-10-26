package claude_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/claude"
	"github.com/sonnes/gopi/pkg/agent/internal/emittest"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
)

// cancelDelay is how long the cancel test waits after a steer, so the
// agent sends the steer before the interrupt.
const cancelDelay = 100 * time.Millisecond

// logTexts returns the role and the text of each message of the log of a.
func logTexts(a *claude.Agent) [][2]string {
	var out [][2]string
	for _, e := range a.Log().Entries() {
		out = append(out, [2]string{string(e.Message.Role), e.Message.Text()})
	}
	return out
}

// steerOn runs a with input, and steers it with text at the first event
// that match accepts. It returns the events and the error of the run.
func steerOn(t *testing.T, a *claude.Agent, input, text string, match func(agent.Event) bool) ([]agent.Event, error) {
	t.Helper()
	s := a.Run(context.Background(), session.Text(input))
	var (
		events  []agent.Event
		steered bool
	)
	for ev, err := range s.Events() {
		if err != nil {
			break
		}
		events = append(events, ev)
		if !steered && match(ev) {
			steered = true
			a.Steer(session.Text(text))
		}
	}
	_, err := s.Wait()
	require.True(t, steered, "the run reached the point of the steer")
	return events, err
}

func isToolStart(ev agent.Event) bool {
	_, ok := ev.(agent.EventToolExecutionStart)
	return ok
}

func TestSteerWhileToolRuns(t *testing.T) {
	claude.Replay(t, nil, "testdata/steer_tool.trace")
	claude.SetUUIDs(t, "steer-1")
	a := claude.New()
	defer closeAgent(t, a)

	events, err := steerOn(t, a, "Run sleep 30", "Reply with: second", isToolStart)
	require.NoError(t, err, "an aborted segment after a steer does not end the run")
	emittest.CheckEvents(t, events)

	assert.Equal(t, [][2]string{
		{"user", "Run sleep 30"},
		{"assistant", ""},
		{"tool_result", "done"},
		{"user", "Reply with: second"},
		{"assistant", "second"},
	}, logTexts(a), "the log keeps the steer where the CLI took it")
	assert.Empty(t, a.TakeSteer())
	checkLog(t, a)
}

func TestSteerWhileTextStreams(t *testing.T) {
	claude.Replay(t, nil, "testdata/steer_stream.trace")
	claude.SetUUIDs(t, "steer-1")
	a := claude.New()
	defer closeAgent(t, a)

	events, err := steerOn(t, a, "Write a poem", "Reply with: second", func(ev agent.Event) bool {
		_, ok := ev.(agent.EventMessageUpdate)
		return ok
	})
	require.NoError(t, err)
	emittest.CheckEvents(t, events)

	assert.Equal(t, [][2]string{
		{"user", "Write a poem"},
		{"user", "Reply with: second"},
		{"assistant", "second"},
	}, logTexts(a), "the cut message has no block that the CLI completed, so the log has none of it")
	checkLog(t, a)
}

func TestSteerMovesASubagentToTheBackground(t *testing.T) {
	claude.Replay(t, nil, "testdata/steer_subagent.trace")
	claude.SetUUIDs(t, "steer-1")
	a := claude.New()
	defer closeAgent(t, a)

	_, err := steerOn(t, a, "Ask a subagent", "Reply with: second", isToolStart)
	require.NoError(t, err, "the background request goes before the steer, as the trace says")

	last := logTexts(a)[len(logTexts(a))-1]
	assert.Equal(t, [2]string{"assistant", "second"}, last)
}

func TestSteerThatTheCLIDidNotTakeComesBack(t *testing.T) {
	claude.Replay(t, nil, "testdata/steer_cancel.trace")
	claude.SetUUIDs(t, "steer-1")
	a := claude.New()
	defer closeAgent(t, a)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := a.Run(ctx, session.Text("Run sleep 30"))
	for ev, err := range s.Events() {
		if err != nil {
			break
		}
		if isToolStart(ev) {
			a.Steer(session.Text("Reply with: second"))
			time.AfterFunc(cancelDelay, cancel)
		}
	}
	_, err := s.Wait()
	assert.ErrorIs(t, err, context.Canceled)

	unread := a.TakeSteer()
	require.Len(t, unread, 1, "the host sends the steer again")
	assert.Equal(t, "Reply with: second", unread[0].Message.Text())
	for _, e := range a.Log().Entries() {
		assert.NotEqual(t, "Reply with: second", e.Message.Text(), "the log has no steer that the CLI did not take")
	}
}

func TestSteerWithNoRunWaitsForTakeSteer(t *testing.T) {
	a := claude.New()

	a.Steer(session.Text("later"))

	unread := a.TakeSteer()
	require.Len(t, unread, 1)
	assert.Equal(t, ai.RoleUser, unread[0].Message.Role)
	assert.Empty(t, a.TakeSteer())
}

var _ agent.Steerer = (*claude.Agent)(nil)
