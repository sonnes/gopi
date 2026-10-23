package codex

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
)

// statusCLI returns the body of a fake CLI that prints version for
// --version, and prints login to stderr and exits with code for login
// status, as Codex does.
func statusCLI(version, login string, code int) string {
	return `case "$1" in
--version) echo '` + version + `' ;;
login) echo '` + login + `' >&2; exit ` + strconv.Itoa(code) + ` ;;
esac`
}

// fakeHome returns a Codex home that holds an auth.json with mode and
// the claims of its ID token, or an empty home if mode is "".
func fakeHome(t *testing.T, mode string, claims map[string]any) string {
	t.Helper()
	dir := t.TempDir()
	if mode == "" {
		return dir
	}
	payload, err := json.Marshal(claims)
	require.NoError(t, err)
	idToken := "e30." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
	auth, err := json.Marshal(map[string]any{
		"auth_mode":      mode,
		"OPENAI_API_KEY": nil,
		"tokens":         map[string]any{"id_token": idToken, "access_token": "secret"},
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "auth.json"), auth, 0o600))
	return dir
}

// chatGPTClaims are the claims of the ID token of a ChatGPT Plus account.
var chatGPTClaims = map[string]any{
	"email": "ada@example.com",
	"https://api.openai.com/auth": map[string]any{
		"chatgpt_plan_type": "plus",
		"organizations": []any{
			map[string]any{"title": "Personal", "is_default": false},
			map[string]any{"title": "Acme", "is_default": true},
		},
	},
}

// checker returns the driver of opts as an agent.Checker.
func checker(t *testing.T, opts ...Option) agent.Checker {
	t.Helper()
	c, ok := Driver(opts...).(agent.Checker)
	require.True(t, ok, "the driver implements agent.Checker")
	return c
}

func TestDriverCheck(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		home    string
		want    agent.Status
		wantErr bool
	}{
		{
			name: "signed in with ChatGPT",
			body: statusCLI("codex-cli 0.163.0", "Logged in using ChatGPT", 0),
			home: fakeHome(t, "chatgpt", chatGPTClaims),
			want: agent.Status{
				Version:  "0.163.0",
				LoggedIn: true,
				Method:   agent.AuthSubscription,
				Plan:     "ChatGPT Plus",
				Account:  "ada@example.com",
				Org:      "Acme",
			},
		},
		{
			name: "signed in with ChatGPT and no auth file",
			body: statusCLI("codex-cli 0.163.0", "Logged in using ChatGPT", 0),
			home: fakeHome(t, "", nil),
			want: agent.Status{
				Version:  "0.163.0",
				LoggedIn: true,
				Method:   agent.AuthSubscription,
				Plan:     "ChatGPT",
			},
		},
		{
			name: "signed in with an API key, which the status never shows",
			body: statusCLI("codex-cli 0.163.0", "Logged in using an API key - sk-proj-***ABCD", 0),
			home: fakeHome(t, "apikey", nil),
			want: agent.Status{
				Version:  "0.163.0",
				LoggedIn: true,
				Method:   agent.AuthAPIKey,
			},
		},
		{
			name: "signed out",
			body: statusCLI("codex-cli 0.163.0", "Not logged in", 1),
			home: fakeHome(t, "", nil),
			want: agent.Status{Version: "0.163.0"},
		},
		{
			name:    "the version fails",
			body:    `echo "error: bad config" >&2; exit 2`,
			home:    fakeHome(t, "", nil),
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := checker(t, Binary(fakeCLI(t, tt.body)), Env("CODEX_HOME="+tt.home))

			got, err := c.Check(t.Context())

			if tt.wantErr {
				assert.Error(t, err)
				assert.False(t, errors.Is(err, agent.ErrNotInstalled))
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDriverCheck_NotInstalled(t *testing.T) {
	c := checker(t, Binary(filepath.Join(t.TempDir(), "codex")))

	_, err := c.Check(t.Context())

	assert.ErrorIs(t, err, agent.ErrNotInstalled)
}

func TestDriverCheck_Env(t *testing.T) {
	body := `case "$1" in
--version) echo 'codex-cli 0.163.0' ;;
login) if [ "$CODEX_HOME" = "$WANT_HOME" ]; then echo 'Logged in using ChatGPT' >&2; else echo 'Not logged in' >&2; exit 1; fi ;;
esac`
	home := fakeHome(t, "chatgpt", chatGPTClaims)
	c := checker(t,
		Binary(fakeCLI(t, body)),
		Env("CODEX_HOME="+home, "WANT_HOME="+home),
	)

	got, err := c.Check(t.Context())

	require.NoError(t, err)
	assert.True(t, got.LoggedIn, "the check runs with the environment of the driver")
	assert.Equal(t, "ada@example.com", got.Account, "the check reads auth.json in the CODEX_HOME of the driver")
}
