package claude_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/claude"
)

func TestDriverLogin(t *testing.T) {
	bin := fakeCLI(t, `echo "$@ $CLAUDE_CONFIG_DIR"`)
	d := claude.Driver(
		claude.Binary(bin),
		claude.Env("CLAUDE_CONFIG_DIR=/work"),
	)
	l, ok := d.(agent.Loginer)
	require.True(t, ok, "the driver implements agent.Loginer")

	out, err := l.Login(t.Context()).Output()

	require.NoError(t, err)
	assert.Equal(t, "auth login --claudeai /work\n", string(out))
}

func TestDriverReadLink(t *testing.T) {
	// The fixture is built from the strings of the Claude Code 2.1.325
	// binary: the link is a terminal hyperlink in a color.
	full, err := os.ReadFile(filepath.Join("testdata", "auth-login-2.1.325.txt"))
	require.NoError(t, err)
	const url = "https://claude.com/cai/oauth/authorize?code=true&client_id=9d1c250a-e61b-44d9-88ed-5944d1962f5e&response_type=code&redirect_uri=http%3A%2F%2Flocalhost%3A53781%2Fcallback&scope=org%3Acreate_api_key+user%3Aprofile+user%3Ainference&code_challenge=Xq3nV0yJt8lQ2kE5bH7rW1sA9dF4gZ6mC0pU2iO8xLw&code_challenge_method=S256&state=m4Rk9TzQ1vB7nY3pL6cW8hJ2sD5fG0aE"
	part, _, _ := bytes.Cut(full, []byte("&state="))
	r, ok := claude.Driver().(agent.LinkReader)
	require.True(t, ok, "the driver implements agent.LinkReader")

	tests := []struct {
		name   string
		output []byte
		want   agent.Link
		wantOK bool
	}{
		{
			name:   "the output of Claude Code 2.1.325",
			output: full,
			want:   agent.Link{URL: url},
			wantOK: true,
		},
		{
			name:   "a plain link with no callback listener",
			output: []byte("Sign in at: " + url + "\r\n"),
			want:   agent.Link{URL: url},
			wantOK: true,
		},
		{
			name:   "a link between the cursor save and restore codes",
			output: []byte("Sign in at: \x1b7" + url + "\x1b8\r\n"),
			want:   agent.Link{URL: url},
			wantOK: true,
		},
		{
			name:   "a hyperlink whose text the terminal wraps",
			output: []byte("visit: \x1b]8;;" + url + "\x07" + url[:60] + "\r\n" + url[60:] + "\x1b]8;;\x07\r\n"),
			want:   agent.Link{URL: url},
			wantOK: true,
		},
		{
			name:   "a part of the link",
			output: part,
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
