package permission_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/permission"
)

// outcome is what a policy says about one call.
type outcome string

const (
	ran    outcome = "run"
	asked  outcome = "ask"
	denied outcome = "deny"
)

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

// decide returns what p says about c.
func decide(t *testing.T, p *permission.Policy, c ai.ToolCall) outcome {
	t.Helper()
	d := p.Decide(c)
	switch d.Match {
	case permission.AllowMatch:
		return ran
	case permission.AskMatch:
		return asked
	case permission.DenyMatch:
		return denied
	}
	require.Failf(t, "no decision", "the policy gave the match %d", d.Match)
	return ""
}

// denyReason returns the reason of the deny of c by p.
func denyReason(t *testing.T, p *permission.Policy, c ai.ToolCall) string {
	t.Helper()
	d := p.Decide(c)
	require.Equal(t, permission.DenyMatch, d.Match)
	return d.Reason
}
