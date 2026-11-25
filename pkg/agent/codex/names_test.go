package codex

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
)

func TestMCPCallKeepsThePrefix(t *testing.T) {
	call, ok := toolCall(item{
		Type:   itemMcpToolCall,
		Server: "docs",
		Tool:   "search",
	})

	require.True(t, ok)
	assert.Equal(t, "mcp__docs__search", call.Name)
}

func TestImageGeneration(t *testing.T) {
	tests := []struct {
		name      string
		item      string
		wantInput string
		wantText  string
		wantError bool
	}{
		{
			name:      "the result is the saved path",
			item:      `{"type":"imageGeneration","id":"ig1","status":"completed","revisedPrompt":"a red fox","result":"iVBORw0KGgo=","savedPath":"/home/me/.codex/generated_images/s1/ig1.png","transparentBackground":null,"failure":null}`,
			wantInput: `{"prompt":"a red fox","path":"/home/me/.codex/generated_images/s1/ig1.png"}`,
			wantText:  "/home/me/.codex/generated_images/s1/ig1.png",
		},
		{
			name:      "a failure is an error",
			item:      `{"type":"imageGeneration","id":"ig1","status":"failed","revisedPrompt":null,"result":"","savedPath":null,"failure":{"type":"usageLimitExceeded","limitId":"images","resetsAt":null}}`,
			wantInput: `{"prompt":null,"path":null}`,
			wantText:  "usageLimitExceeded: images",
			wantError: true,
		},
		{
			name:      "a failure with no limit has no separator",
			item:      `{"type":"imageGeneration","id":"ig1","status":"failed","revisedPrompt":null,"result":"","savedPath":null,"failure":{"type":"contentPolicy","limitId":null,"resetsAt":null}}`,
			wantInput: `{"prompt":null,"path":null}`,
			wantText:  "contentPolicy",
			wantError: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var it item
			require.NoError(t, json.Unmarshal([]byte(tt.item), &it))

			call, ok := toolCall(it)
			require.True(t, ok)
			assert.Equal(t, "ig1", call.ID)
			assert.Equal(t, "image_generation", call.Name)
			assert.JSONEq(t, tt.wantInput, string(call.Input), "the input never holds the image data")

			result := toolResult(it)
			assert.Equal(t, []ai.Content{ai.Text{Text: tt.wantText}}, result.Content)
			assert.Equal(t, tt.wantError, result.IsError)
		})
	}
}
