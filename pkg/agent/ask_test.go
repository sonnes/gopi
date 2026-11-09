package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
)

func TestAskHook(t *testing.T) {
	asks := agent.BeforeTool(func(_ context.Context, _ agent.State, c ai.ToolCall) (ai.ToolCall, error) {
		return c, agent.Ask("the rule asks")
	})
	tests := []struct {
		name     string
		approve  agent.ApproveFunc
		want     string
		isError  bool
		wantSeen bool
	}{
		{
			name: "an allow runs the call",
			approve: func(context.Context, agent.ApprovalRequest) (agent.Decision, error) {
				return agent.Decision{Allow: true}, nil
			},
			want:     "echo:x",
			wantSeen: true,
		},
		{
			name: "a new input runs the call with it",
			approve: func(context.Context, agent.ApprovalRequest) (agent.Decision, error) {
				return agent.Decision{
					Allow: true,
					Input: json.RawMessage(`{"s":"y"}`),
				}, nil
			},
			want:     "echo:y",
			wantSeen: true,
		},
		{
			name: "a deny blocks the call with its message",
			approve: func(context.Context, agent.ApprovalRequest) (agent.Decision, error) {
				return agent.Decision{Message: "not now"}, nil
			},
			want:     "not now",
			isError:  true,
			wantSeen: true,
		},
		{
			name: "an error blocks the call",
			approve: func(context.Context, agent.ApprovalRequest) (agent.Decision, error) {
				return agent.Decision{}, errors.New("no person")
			},
			want:     "no person",
			isError:  true,
			wantSeen: true,
		},
		{
			name:    "no approve function blocks the call",
			want:    "approve",
			isError: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &script{responses: []ai.Message{
				toolCall("c1", "echo", `{"s":"x"}`),
				text("done"),
			}}
			var seen []agent.ApprovalRequest
			opts := []agent.Option{
				agent.Toolset(echo),
				agent.Hooks(asks),
			}
			if tt.approve != nil {
				opts = append(opts, agent.Approve(func(ctx context.Context, req agent.ApprovalRequest) (agent.Decision, error) {
					seen = append(seen, req)
					return tt.approve(ctx, req)
				}))
			}
			a := agent.New(model(p), opts...)

			_, msgs, err := collect(t, a.Run(context.Background(), session.Text("go")))

			require.NoError(t, err)
			i := slices.IndexFunc(msgs, func(m ai.Message) bool { return m.Role == ai.RoleToolResult })
			require.GreaterOrEqual(t, i, 0, "the run has a tool result")
			result := msgs[i]
			assert.Contains(t, result.Text(), tt.want)
			assert.Equal(t, tt.isError, result.IsError)
			assert.Equal(t, tt.wantSeen, len(seen) == 1)
			if tt.wantSeen {
				assert.Equal(t, "the rule asks", seen[0].Reason)
				assert.Equal(t, "c1", seen[0].Call.ID)
			}
		})
	}
}

func TestAskErrorText(t *testing.T) {
	var ask *agent.AskError
	err := agent.Ask("")

	require.True(t, errors.As(err, &ask))
	assert.Equal(t, "the tool call needs a person", err.Error())
}
