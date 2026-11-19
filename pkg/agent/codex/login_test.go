package codex

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
)

func TestDriverLogin(t *testing.T) {
	bin := fakeCLI(t, `echo "$@ $CODEX_HOME"`)
	d := Driver(
		Binary(bin),
		Env("CODEX_HOME=/work"),
	)
	l, ok := d.(agent.Loginer)
	require.True(t, ok, "the driver implements agent.Loginer")

	out, err := l.Login(t.Context()).Output()

	require.NoError(t, err)
	assert.Equal(t, "login --device-auth /work\n", string(out))
}

func TestDriverReadLink(t *testing.T) {
	full, err := os.ReadFile(filepath.Join("testdata", "device-auth-0.163.0.txt"))
	require.NoError(t, err)
	noCode, _, _ := bytes.Cut(full, []byte("QBVZ"))
	r, ok := Driver().(agent.LinkReader)
	require.True(t, ok, "the driver implements agent.LinkReader")

	tests := []struct {
		name   string
		output []byte
		want   agent.Link
		wantOK bool
	}{
		{
			name:   "the output of Codex 0.163.0",
			output: full,
			want: agent.Link{
				URL:  "https://auth.openai.com/codex/device",
				Code: "QBVZ-JYHN6",
			},
			wantOK: true,
		},
		{
			name:   "a link between the cursor save and restore codes",
			output: []byte("Open \x1b7https://auth.openai.com/codex/device\x1b8 and enter QBVZ-JYHN6\r\n"),
			want: agent.Link{
				URL:  "https://auth.openai.com/codex/device",
				Code: "QBVZ-JYHN6",
			},
			wantOK: true,
		},
		{
			name:   "the link without the code",
			output: noCode,
		},
		{
			name: "no output",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := r.ReadLink(tt.output)

			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}
