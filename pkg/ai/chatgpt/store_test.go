package chatgpt_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai/chatgpt"
	"github.com/sonnes/gopi/pkg/ai/oauth"
)

func testAccount(expiry time.Time) chatgpt.Account {
	return chatgpt.Account{
		ClientID: issuedClientID,
		HostID:   testHostID,
		Token: oauth.Token{
			Access:  "access-0",
			Refresh: "refresh-0",
			Expiry:  expiry,
			Scopes:  []string{"openid", "chatgpt.tokens.use.direct"},
		},
		IDToken: "id",
		Subject: "user-1",
		Email:   "you@example.com",
	}
}

func TestStore_SaveAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"other":{"keep":true}}`), 0o600))
	want := testAccount(time.Now().Add(time.Hour).Truncate(time.Second))

	require.NoError(t, chatgpt.SaveAccount(path, want))
	got, err := chatgpt.LoadAccount(path)

	require.NoError(t, err)
	assert.Equal(t, want.ClientID, got.ClientID)
	assert.Equal(t, want.Token.Refresh, got.Token.Refresh)
	assert.True(t, want.Token.Expiry.Equal(got.Token.Expiry))
	assert.Equal(t, want.Token.Scopes, got.Token.Scopes)
	assert.Equal(t, want.Email, got.Email)

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	var file map[string]json.RawMessage
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &file))
	assert.JSONEq(t, `{"keep":true}`, string(file["other"]))
	assert.Contains(t, string(file["chatgpt"]), `"refresh_token"`)
}

func TestStore_LoadMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")

	_, err := chatgpt.LoadAccount(path)

	require.ErrorIs(t, err, chatgpt.ErrNoAccount)
}

func TestAccount_Registration(t *testing.T) {
	a := testAccount(time.Now())

	r := a.Registration()

	assert.Equal(t, a.ClientID, r.ClientID)
	assert.Equal(t, a.HostID, r.HostID)
	assert.False(t, r.SignedIn())
	assert.True(t, a.SignedIn())
	assert.Empty(t, r.IDToken)
}

func TestFileSource_Valid(t *testing.T) {
	f := newFakeOpenAI(t)
	path := filepath.Join(t.TempDir(), "auth.json")
	require.NoError(t, chatgpt.SaveAccount(path, testAccount(time.Now().Add(time.Hour))))

	tok, err := chatgpt.FileSource(path, f.auth()).Token(t.Context())

	require.NoError(t, err)
	assert.Equal(t, "access-0", tok.Access)
	assert.Zero(t, f.refreshes.Load())
}

func TestFileSource_OneRefreshForTwoSources(t *testing.T) {
	f := newFakeOpenAI(t)
	path := filepath.Join(t.TempDir(), "auth.json")
	require.NoError(t, chatgpt.SaveAccount(path, testAccount(time.Now().Add(time.Minute))))

	// Two sources on one file stand for two gopi processes.
	sources := []oauth.Source{
		chatgpt.FileSource(path, f.auth()),
		chatgpt.FileSource(path, f.auth()),
	}
	tokens := make([]oauth.Token, len(sources))
	errs := make([]error, len(sources))
	var wg sync.WaitGroup
	for i, src := range sources {
		wg.Go(func() {
			tokens[i], errs[i] = src.Token(t.Context())
		})
	}
	wg.Wait()

	for i := range sources {
		require.NoError(t, errs[i])
		assert.Equal(t, "access-1", tokens[i].Access)
	}
	assert.Equal(t, int32(1), f.refreshes.Load())
	saved, err := chatgpt.LoadAccount(path)
	require.NoError(t, err)
	assert.Equal(t, "refresh-1", saved.Token.Refresh)
}

func TestFileSource_SignedOut(t *testing.T) {
	f := newFakeOpenAI(t)
	path := filepath.Join(t.TempDir(), "auth.json")
	require.NoError(t, chatgpt.SaveAccount(path, testAccount(time.Now()).Registration()))

	_, err := chatgpt.FileSource(path, f.auth()).Token(t.Context())

	require.ErrorIs(t, err, chatgpt.ErrNoAccount)
}
