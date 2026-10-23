package claude_test

import (
	"errors"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/claude"
)

// statusCLI returns the body of a fake CLI that prints version for
// --version, and prints auth and exits with code for auth status.
func statusCLI(version, auth string, code int) string {
	return `case "$1" in
--version) echo '` + version + `' ;;
auth) echo '` + auth + `'; exit ` + strconv.Itoa(code) + ` ;;
esac`
}

// checker returns the driver of opts as an agent.Checker.
func checker(t *testing.T, opts ...claude.Option) agent.Checker {
	t.Helper()
	c, ok := claude.Driver(opts...).(agent.Checker)
	require.True(t, ok, "the driver implements agent.Checker")
	return c
}

func TestDriverCheck(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    agent.Status
		wantErr bool
	}{
		{
			name: "signed in with a subscription",
			body: statusCLI(
				"2.1.308 (Claude Code)",
				`{"loggedIn": true, "authMethod": "claude.ai", "apiProvider": "firstParty", "email": "ada@example.com", "orgName": "Acme", "subscriptionType": "max"}`,
				0,
			),
			want: agent.Status{
				Version:  "2.1.308",
				LoggedIn: true,
				Method:   agent.AuthSubscription,
				Plan:     "Claude Max",
				Account:  "ada@example.com",
				Org:      "Acme",
			},
		},
		{
			name: "signed in with a long-lived token",
			body: statusCLI(
				"2.1.308 (Claude Code)",
				`{"loggedIn": true, "authMethod": "oauth_token", "apiProvider": "firstParty"}`,
				0,
			),
			want: agent.Status{
				Version:  "2.1.308",
				LoggedIn: true,
				Method:   agent.AuthSubscription,
			},
		},
		{
			name: "signed in with an API key",
			body: statusCLI(
				"2.1.308 (Claude Code)",
				`{"loggedIn": true, "authMethod": "api_key", "apiProvider": "firstParty", "apiKeySource": "ANTHROPIC_API_KEY"}`,
				0,
			),
			want: agent.Status{
				Version:  "2.1.308",
				LoggedIn: true,
				Method:   agent.AuthAPIKey,
			},
		},
		{
			name: "signed in with an API key helper",
			body: statusCLI(
				"2.1.308 (Claude Code)",
				`{"loggedIn": true, "authMethod": "api_key_helper", "apiProvider": "firstParty"}`,
				0,
			),
			want: agent.Status{
				Version:  "2.1.308",
				LoggedIn: true,
				Method:   agent.AuthAPIKey,
			},
		},
		{
			name: "signed in through a cloud",
			body: statusCLI(
				"2.1.308 (Claude Code)",
				`{"loggedIn": true, "authMethod": "third_party", "apiProvider": "bedrock"}`,
				0,
			),
			want: agent.Status{
				Version:  "2.1.308",
				LoggedIn: true,
				Method:   agent.AuthCloud,
				Plan:     "Amazon Bedrock",
			},
		},
		{
			name: "signed out",
			body: statusCLI(
				"2.1.308 (Claude Code)",
				`{"loggedIn": false, "authMethod": "none"}`,
				1,
			),
			want: agent.Status{Version: "2.1.308"},
		},
		{
			name:    "the version fails",
			body:    `echo "error: bad config" >&2; exit 2`,
			wantErr: true,
		},
		{
			name:    "the auth status is not JSON",
			body:    statusCLI("2.1.308 (Claude Code)", "unknown command", 1),
			want:    agent.Status{Version: "2.1.308"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := checker(t, claude.Binary(fakeCLI(t, tt.body)))

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
	c := checker(t, claude.Binary(filepath.Join(t.TempDir(), "claude")))

	_, err := c.Check(t.Context())

	assert.ErrorIs(t, err, agent.ErrNotInstalled)
}

func TestDriverCheck_Env(t *testing.T) {
	body := `case "$1" in
--version) echo '2.1.308 (Claude Code)' ;;
auth) if [ "$CLAUDE_CONFIG_DIR" = /work ]; then echo '{"loggedIn": true}'; else echo '{"loggedIn": false}'; fi ;;
esac`
	c := checker(t,
		claude.Binary(fakeCLI(t, body)),
		claude.Env("CLAUDE_CONFIG_DIR=/work"),
	)

	got, err := c.Check(t.Context())

	require.NoError(t, err)
	assert.True(t, got.LoggedIn, "the check runs with the environment of the driver")
}
