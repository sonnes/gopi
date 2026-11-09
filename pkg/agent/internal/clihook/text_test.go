package clihook_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent/internal/clihook"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
)

func TestEntries(t *testing.T) {
	custom, err := session.Custom("note", "x")
	require.NoError(t, err)
	file := session.NewEntry(ai.Message{
		Role:    ai.RoleUser,
		Content: []ai.Content{ai.File{MediaType: "image/png"}},
	})

	tests := []struct {
		name    string
		entries []session.Entry
		want    string
		wantErr error
	}{
		{
			name:    "text entries join with a blank line",
			entries: []session.Entry{session.Text("one"), session.Text("two")},
			want:    "one\n\ntwo",
		},
		{
			name:    "a custom entry is not text",
			entries: []session.Entry{custom},
			wantErr: clihook.ErrNotText,
		},
		{
			name:    "a file is not text",
			entries: []session.Entry{file},
			wantErr: clihook.ErrNotText,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := clihook.Entries(tt.entries)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestResponse(t *testing.T) {
	assert.Equal(t, "done", clihook.Response(json.RawMessage(`"done"`)))
	assert.Equal(t, `{"stdout":"a"}`, clihook.Response(json.RawMessage(`{"stdout":"a"}`)))
}
