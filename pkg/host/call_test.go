package host_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/host"
	"github.com/sonnes/gopi/pkg/session"
)

// assertGone fails the test if the host or its store has a session.
func assertGone(t *testing.T, f fixture) {
	t.Helper()
	assert.Empty(t, f.h.List(), "the host has no session")
	snaps, err := f.store.List(context.Background())
	require.NoError(t, err)
	assert.Empty(t, snaps, "the store has no record")
}

func TestCallReturnsTheReply(t *testing.T) {
	f := newFixture(t)
	f.model.replies = []ai.Message{reply("hello")}

	msg, err := f.h.Call(t.Context(), host.Config{Model: "fake/m"}, host.Text("hi"))

	require.NoError(t, err)
	assert.Equal(t, "hello", msg.Text())
	assert.Equal(t, ai.RoleAssistant, msg.Role)
	assert.Equal(t, 10, msg.Usage.Input, "the reply has its usage")
	assert.Equal(t, []string{"hi"}, f.model.texts())
	assertGone(t, f)
}

func TestCallKeepsNoSession(t *testing.T) {
	f := newFixture(t)
	f.model.replies = []ai.Message{reply("hello")}

	_, err := f.h.Call(t.Context(), host.Config{Model: "fake/m"}, host.Text("hi"))
	require.NoError(t, err)

	config := f.lastConfig(t)
	assert.Equal(t, session.Discard{}, config.Store, "the agent writes to Discard")
	assert.NotEmpty(t, config.SessionID)
}

func TestCallAfterClose(t *testing.T) {
	f := newFixture(t)
	require.NoError(t, f.h.Close())

	_, err := f.h.Call(t.Context(), host.Config{Model: "fake/m"}, host.Text("hi"))

	require.ErrorIs(t, err, host.ErrClosed)
}

func TestCloseStopsACall(t *testing.T) {
	f, _ := gated(t)
	errs := make(chan error, 1)
	go func() {
		_, err := f.h.Call(context.Background(), host.Config{Model: "fake/m"}, host.Text("hi"))
		errs <- err
	}()
	f.model.waitCalls(t, 1)

	require.NoError(t, f.h.Close())

	require.Error(t, <-errs, "Close waits for the call, and the call ends with an error")
}

func TestCallReturnsTheErrorOfTheRun(t *testing.T) {
	f := newFixture(t)
	f.model.fail = errors.New("boom")

	_, err := f.h.Call(t.Context(), host.Config{Model: "fake/m"}, host.Text("hi"))

	require.ErrorContains(t, err, "boom")
	assertGone(t, f)
}

func TestCallOfABadConfig(t *testing.T) {
	f := newFixture(t)

	_, err := f.h.Call(t.Context(), host.Config{Model: "fake/m", Effort: "loud"}, host.Text("hi"))

	require.ErrorIs(t, err, host.ErrInvalid)
	assertGone(t, f)
}

func TestCallWhenTheAgentFailsToStart(t *testing.T) {
	f := newFixture(t)
	f.driver.setErr(errors.New("no agent"))

	_, err := f.h.Call(t.Context(), host.Config{Model: "fake/m"}, host.Text("hi"))

	require.ErrorContains(t, err, "no agent")
	assertGone(t, f)
}

func TestCallStopsWhenTheContextEnds(t *testing.T) {
	f, _ := gated(t)
	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		f.model.waitCalls(t, 1)
		cancel()
	}()

	_, err := f.h.Call(ctx, host.Config{Model: "fake/m"}, host.Text("hi"))

	require.ErrorIs(t, err, context.Canceled)
	assertGone(t, f)
}
