package chatgpt_test

import (
	"crypto/rand"
	"crypto/rsa"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai/chatgpt"
	"github.com/sonnes/gopi/pkg/ai/oauth"
)

const testHostID = "urn:uuid:00000000-0000-4000-8000-000000000000"

// signIn runs a sign-in against f on a free port.
func signIn(t *testing.T, f *fakeOpenAI, prior *chatgpt.Account) (chatgpt.Account, error) {
	t.Helper()
	cfg := chatgpt.SignInConfig{
		AppName: "gopi",
		HostID:  testHostID,
		Port:    0,
		Open:    f.open,
	}
	return f.auth().SignIn(t.Context(), cfg, prior)
}

func TestSignIn_First(t *testing.T) {
	f := newFakeOpenAI(t)

	a, err := signIn(t, f, nil)

	require.NoError(t, err)
	assert.Equal(t, issuedClientID, a.ClientID)
	assert.Equal(t, testHostID, a.HostID)
	assert.Equal(t, "access-0", a.Token.Access)
	assert.Equal(t, "refresh-0", a.Token.Refresh)
	assert.Contains(t, a.Token.Scopes, "chatgpt.tokens.use.direct")
	assert.Equal(t, "user-1", a.Subject)
	assert.Equal(t, "you@example.com", a.Email)
	assert.NotEmpty(t, a.IDToken)

	q := f.authorize
	assert.Equal(t, "dynamic_agent_client", q.Get("client_id"))
	assert.Equal(t, "gopi", q.Get("agent_name_hint"))
	assert.Equal(t, testHostID, q.Get("ext_agent_host_id"))
	assert.Equal(t, "https://api.openai.com/v1", q.Get("resource"))
	assert.Equal(t, "S256", q.Get("code_challenge_method"))
	assert.True(t, strings.HasPrefix(q.Get("redirect_uri"), "http://127.0.0.1:"))
	assert.True(t, scopeHas(q.Get("scope"), "chatgpt.tokens.use.direct"))
	assert.NotEmpty(t, q.Get("nonce"))

	exchange := f.tokenForms[0]
	assert.Equal(t, "authorization_code", exchange.Get("grant_type"))
	assert.Equal(t, issuedClientID, exchange.Get("client_id"))
	assert.Equal(t, "the-code", exchange.Get("code"))
	assert.Equal(t, q.Get("redirect_uri"), exchange.Get("redirect_uri"))
	assert.Equal(t, "https://api.openai.com/v1", exchange.Get("resource"))
	assert.NotEmpty(t, exchange.Get("code_verifier"))
}

func TestSignIn_Again(t *testing.T) {
	f := newFakeOpenAI(t)
	prior := chatgpt.Account{
		ClientID: issuedClientID,
		HostID:   "urn:uuid:11111111-1111-4111-8111-111111111111",
		IDToken:  "old-id-token",
		Email:    "you@example.com",
	}

	a, err := signIn(t, f, &prior)

	require.NoError(t, err)
	assert.Equal(t, issuedClientID, a.ClientID)
	assert.Equal(t, prior.HostID, a.HostID)
	q := f.authorize
	assert.Equal(t, issuedClientID, q.Get("client_id"))
	assert.Equal(t, prior.HostID, q.Get("ext_agent_host_id"))
	assert.Equal(t, "old-id-token", q.Get("id_token_hint"))
	assert.Equal(t, "you@example.com", q.Get("login_hint"))
	assert.False(t, q.Has("agent_name_hint"))
}

func TestSignIn_Refused(t *testing.T) {
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	tests := []struct {
		name  string
		setup func(f *fakeOpenAI)
		want  string
	}{
		{
			name:  "no issued client ID",
			setup: func(f *fakeOpenAI) { f.noClientID = true },
			want:  "client_id",
		},
		{
			name:  "another signing key",
			setup: func(f *fakeOpenAI) { f.signWith = otherKey },
			want:  "signature",
		},
		{
			name:  "another nonce",
			setup: func(f *fakeOpenAI) { f.nonce = "other" },
			want:  "nonce",
		},
		{
			name:  "no plan scope",
			setup: func(f *fakeOpenAI) { f.scope = "openid profile email offline_access" },
			want:  "chatgpt.tokens.use.direct",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeOpenAI(t)
			tt.setup(f)

			_, err := signIn(t, f, nil)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestRefresh(t *testing.T) {
	f := newFakeOpenAI(t)
	a := chatgpt.Account{
		ClientID: issuedClientID,
		HostID:   testHostID,
		Token:    oauth.Token{Access: "old", Refresh: "refresh-0"},
		Email:    "you@example.com",
	}

	got, err := f.auth().Refresh(t.Context(), a)

	require.NoError(t, err)
	assert.Equal(t, "access-1", got.Token.Access)
	assert.Equal(t, "refresh-1", got.Token.Refresh)
	assert.True(t, got.Token.Valid())
	assert.Equal(t, "you@example.com", got.Email)

	form := f.tokenForms[0]
	assert.Equal(t, "refresh_token", form.Get("grant_type"))
	assert.Equal(t, issuedClientID, form.Get("client_id"))
	assert.Equal(t, "refresh-0", form.Get("refresh_token"))
	assert.Equal(t, "https://api.openai.com/v1", form.Get("resource"))
	assert.False(t, form.Has("scope"))
}

func TestRefresh_Expired(t *testing.T) {
	f := newFakeOpenAI(t)
	f.refreshError = "invalid_grant"
	a := chatgpt.Account{
		ClientID: issuedClientID,
		Token:    oauth.Token{Refresh: "refresh-0"},
	}

	_, err := f.auth().Refresh(t.Context(), a)

	require.ErrorIs(t, err, chatgpt.ErrSignInExpired)
}

func TestSignOut(t *testing.T) {
	f := newFakeOpenAI(t)
	a := chatgpt.Account{
		ClientID: issuedClientID,
		Token:    oauth.Token{Access: "a", Refresh: "refresh-0"},
	}

	err := f.auth().SignOut(t.Context(), a)

	require.NoError(t, err)
	require.Len(t, f.revokeForms, 1)
	form := f.revokeForms[0]
	assert.Equal(t, "refresh-0", form.Get("token"))
	assert.Equal(t, "refresh_token", form.Get("token_type_hint"))
	assert.Equal(t, issuedClientID, form.Get("client_id"))
}

func TestNewHostID(t *testing.T) {
	id := chatgpt.NewHostID()

	assert.Regexp(t, `^urn:uuid:[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`, id)
	assert.NotEqual(t, id, chatgpt.NewHostID())
}
