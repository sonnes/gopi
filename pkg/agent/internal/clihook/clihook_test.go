package clihook_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sonnes/gopi/pkg/agent/internal/clihook"
)

func TestSameInput(t *testing.T) {
	tests := []struct {
		name string
		a    string
		b    string
		want bool
	}{
		{name: "the same bytes", a: `{"command":"ls"}`, b: `{"command":"ls"}`, want: true},
		{name: "other spaces and key order", a: `{"a":1,"b":2}`, b: `{ "b": 2, "a": 1 }`, want: true},
		{name: "another value", a: `{"command":"ls"}`, b: `{"command":"ls -a"}`, want: false},
		{name: "invalid JSON that differs", a: `{`, b: `{}`, want: false},
		{name: "no input on both sides", a: ``, b: ``, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := clihook.SameInput(json.RawMessage(tt.a), json.RawMessage(tt.b))
			assert.Equal(t, tt.want, got)
		})
	}
}
