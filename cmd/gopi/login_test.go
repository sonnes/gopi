package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai/chatgpt"
	"github.com/sonnes/gopi/pkg/ai/oauth"
	"github.com/sonnes/gopi/pkg/catalog"
)

func signedIn() chatgpt.Account {
	return chatgpt.Account{
		ClientID: "oaiapp_test",
		HostID:   "urn:uuid:00000000-0000-4000-8000-000000000000",
		Token: oauth.Token{
			Access:  "a",
			Refresh: "r",
			Expiry:  time.Now().Add(time.Hour),
		},
		Email: "you@example.com",
	}
}

func TestDetectors_ChatGPTLogin(t *testing.T) {
	tests := []struct {
		name    string
		account *chatgpt.Account
		want    bool
	}{
		{
			name: "no login file",
		},
		{
			name:    "signed in",
			account: new(signedIn()),
			want:    true,
		},
		{
			name:    "signed out",
			account: new(signedIn().Registration()),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			if tt.account != nil {
				require.NoError(t, chatgpt.SaveAccount(authPath(home), *tt.account))
			}

			ds := detectors(home, repoFile)
			found := catalog.New().Detect(ds...)

			assert.Equal(t, "chatgpt", ds[0].Provider)
			var got bool
			for _, f := range found {
				got = got || f.Provider == "chatgpt"
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestAuthPath(t *testing.T) {
	assert.Equal(t, filepath.Join("/home/u/.gopi", "auth.json"), authPath("/home/u/.gopi"))
}

func TestLoginProvider(t *testing.T) {
	tests := []struct {
		args    []string
		wantErr string
	}{
		{args: []string{"chatgpt"}},
		{args: []string{"ChatGPT"}},
		{args: nil, wantErr: "usage"},
		{args: []string{"anthropic"}, wantErr: "claude-code"},
		{args: []string{"other"}, wantErr: "chatgpt"},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			err := loginProvider("login", tt.args)

			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}
