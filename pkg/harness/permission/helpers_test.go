package permission_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/harness/permission"
)

// outcome is what the hook did with one call.
type outcome string

const (
	ran    outcome = "run"
	asked  outcome = "ask"
	denied outcome = "deny"
)

// approver records the calls that it gets, and returns one decision.
type approver struct {
	decision permission.Decision
	calls    []ai.ToolCall
}

func (a *approver) approve(_ context.Context, c ai.ToolCall) (permission.Decision, error) {
	a.calls = append(a.calls, c)
	return a.decision, nil
}

// call returns a tool call with the input as JSON.
func call(t *testing.T, name string, input map[string]any) ai.ToolCall {
	t.Helper()
	data, err := json.Marshal(input)
	require.NoError(t, err)
	return ai.ToolCall{
		ID:    "call_1",
		Name:  name,
		Input: data,
	}
}

func bashCall(t *testing.T, command string) ai.ToolCall {
	t.Helper()
	return call(t, "bash", map[string]any{"command": command})
}

func pathCall(t *testing.T, name, path string) ai.ToolCall {
	t.Helper()
	return call(t, name, map[string]any{"path": path})
}

// decide runs the hook of p on c. An approver that allows each call
// tells an ask from a run. The approver of p, if set, is replaced.
func decide(t *testing.T, p *permission.Policy, c ai.ToolCall) outcome {
	t.Helper()
	a := &approver{decision: permission.Allow}
	p.Approve = a.approve
	out := runHook(t, p, c)
	if len(a.calls) > 0 {
		require.Equal(t, ran, out)
		return asked
	}
	return out
}

// runHook runs a new hook of p on c, with the approver of p.
func runHook(t *testing.T, p *permission.Policy, c ai.ToolCall) outcome {
	t.Helper()
	return runOn(t, p.Hook(), c)
}

// runOn runs the hook h on c.
func runOn(t *testing.T, h agent.Hook, c ai.ToolCall) outcome {
	t.Helper()
	got, err := h.BeforeTool(context.Background(), agent.State{}, c)
	if err == nil {
		require.Equal(t, c, got)
		return ran
	}
	var deny *agent.DenyError
	require.True(t, errors.As(err, &deny), "error %v is not a DenyError", err)
	return denied
}

// denyReason runs the hook of p on c, and returns the reason of the
// deny.
func denyReason(t *testing.T, p *permission.Policy, c ai.ToolCall) string {
	t.Helper()
	_, err := p.Hook().BeforeTool(context.Background(), agent.State{}, c)
	var deny *agent.DenyError
	require.True(t, errors.As(err, &deny), "error %v is not a DenyError", err)
	return deny.Reason
}
