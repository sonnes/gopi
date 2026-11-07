package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
)

// bashCall returns a call of bash with the command command.
func bashCall(id, command string) ai.ToolCall {
	input, _ := json.Marshal(map[string]string{"command": command})
	return ai.ToolCall{ID: id, Name: "bash", Input: input}
}

func TestGrantsKeepTheRuleOfAlways(t *testing.T) {
	var asked []agent.ApprovalRequest
	g := &grants{
		dir: t.TempDir(),
		ask: func(_ context.Context, req agent.ApprovalRequest) (agent.Decision, error) {
			asked = append(asked, req)
			return agent.Decision{Allow: true, Always: true}, nil
		},
	}
	ctx := context.Background()

	first, err := g.Approve(ctx, agent.ApprovalRequest{Call: bashCall("c1", "go test ./...")})
	require.NoError(t, err)
	second, err := g.Approve(ctx, agent.ApprovalRequest{Call: bashCall("c2", "go test ./...")})
	require.NoError(t, err)
	_, err = g.Approve(ctx, agent.ApprovalRequest{Call: bashCall("c3", "go vet ./...")})
	require.NoError(t, err)

	assert.Equal(t, agent.Decision{Allow: true}, first, "the harness gets no Always")
	assert.Equal(t, agent.Decision{Allow: true}, second)
	require.Len(t, asked, 2, "the grant allows the same command with no question")
	assert.Equal(t, "Bash(go test ./...)", asked[0].Rule)
	assert.Equal(t, "c3", asked[1].Call.ID)
}

func TestGrantsAskForEachSafetyCheck(t *testing.T) {
	var asked []agent.ApprovalRequest
	g := &grants{
		dir: t.TempDir(),
		ask: func(_ context.Context, req agent.ApprovalRequest) (agent.Decision, error) {
			asked = append(asked, req)
			return agent.Decision{Allow: true, Always: true}, nil
		},
	}
	ctx := context.Background()
	req := agent.ApprovalRequest{
		Call:   bashCall("c1", "rm .git/config"),
		Safety: true,
	}

	_, err := g.Approve(ctx, req)
	require.NoError(t, err)
	_, err = g.Approve(ctx, req)
	require.NoError(t, err)

	require.Len(t, asked, 2, "a safety check always asks")
	assert.Empty(t, asked[0].Rule, "a safety check has no rule to keep")
}
