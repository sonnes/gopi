package agent_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
)

// A decision in JSON keeps the input of the call unless it names a new
// input. A null input names no new input.
func TestDecisionJSON(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want agent.Decision
	}{
		{
			name: "no input",
			in:   `{"allow":true}`,
			want: agent.Decision{Allow: true},
		},
		{
			name: "null input",
			in:   `{"allow":true,"input":null}`,
			want: agent.Decision{Allow: true},
		},
		{
			name: "new input",
			in:   `{"allow":true,"input":{"command":"ls"}}`,
			want: agent.Decision{
				Allow: true,
				Input: json.RawMessage(`{"command":"ls"}`),
			},
		},
		{
			name: "deny",
			in:   `{"message":"no"}`,
			want: agent.Decision{Message: "no"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got agent.Decision
			require.NoError(t, json.Unmarshal([]byte(tt.in), &got))
			assert.Equal(t, tt.want, got)
		})
	}
}
