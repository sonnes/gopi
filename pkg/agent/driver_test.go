package agent_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
)

// An agent is a runner, and a caller can steer it.
var (
	_ agent.Runner  = (*agent.Agent)(nil)
	_ agent.Steerer = (*agent.Agent)(nil)
)

// stopHook is a hook at BeforeStop only.
type stopHook struct{ agent.BaseHook }

func (stopHook) BeforeStop(context.Context, agent.State) ([]session.Entry, error) {
	return nil, nil
}

// toolHook is a hook at BeforeTool only.
type toolHook struct{ agent.BaseHook }

func (toolHook) BeforeTool(_ context.Context, _ agent.State, c ai.ToolCall) (ai.ToolCall, error) {
	return c, nil
}

func TestPoints(t *testing.T) {
	tests := []struct {
		name string
		hook agent.Hook
		want []agent.Point
	}{
		{
			name: "no point",
			hook: agent.BaseHook{},
			want: nil,
		},
		{
			name: "two points in the order of the loop",
			hook: policy{},
			want: []agent.Point{agent.PointBeforeCall, agent.PointBeforeTool},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, agent.Points(tt.hook))
		})
	}
}

func TestCheckHooks(t *testing.T) {
	tests := []struct {
		name        string
		hooks       []agent.Hook
		supported   []agent.Point
		unsupported bool
		errText     string
	}{
		{
			name:      "no hooks",
			hooks:     nil,
			supported: nil,
		},
		{
			name:      "each point is supported",
			hooks:     []agent.Hook{stopHook{}, toolHook{}},
			supported: []agent.Point{agent.PointBeforeTool, agent.PointBeforeStop},
		},
		{
			name:        "a point that is not supported",
			hooks:       []agent.Hook{stopHook{}, toolHook{}},
			supported:   []agent.Point{agent.PointBeforeStop},
			unsupported: true,
			errText:     "agent: hook 2 (agent_test.toolHook) uses BeforeTool: unsupported operation",
		},
		{
			name:      "a hook with no point",
			hooks:     []agent.Hook{agent.BaseHook{}},
			supported: agent.AllPoints,
			errText:   "agent: hook 1 (agent.BaseHook) implements no point",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := agent.CheckHooks(tt.hooks, tt.supported...)
			if tt.errText == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tt.errText)
			assert.Equal(t, tt.unsupported, errors.Is(err, errors.ErrUnsupported))
		})
	}
}
