package family

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOf(t *testing.T) {
	table := map[string]string{
		"claude-sonnet-5":   "sonnet 5",
		"claude-sonnet-5-5": "sonnet 5.5",
	}
	tests := []struct {
		id     string
		want   string
		wantOK bool
	}{
		{id: "claude-sonnet-5", want: "sonnet 5", wantOK: true},
		{id: "claude-sonnet-5-20260101", want: "sonnet 5", wantOK: true},
		{id: "claude-sonnet-5-5", want: "sonnet 5.5", wantOK: true},
		{id: "claude-sonnet-5-5-20260901", want: "sonnet 5.5", wantOK: true},
		{id: "claude-sonnet-50"},
		{id: "claude-opus-5"},
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			got, ok := Of(table, tt.id)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGPT(t *testing.T) {
	for _, id := range []string{"gpt-6-luna", "gpt-6.1-sol", "gpt-6.2-astra", "gpt-5.6", "gpt-5.6-terra"} {
		assert.True(t, GPT(id), id)
	}
	for _, id := range []string{"gpt-5.4-mini", "gpt-image-2", "gpt-60", "o3"} {
		assert.False(t, GPT(id), id)
	}
}
