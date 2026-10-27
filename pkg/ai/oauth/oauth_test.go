package oauth_test

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/sonnes/gopi/pkg/ai/oauth"
)

func TestToken_Valid(t *testing.T) {
	tests := []struct {
		name  string
		token oauth.Token
		want  bool
	}{
		{
			name:  "no access token",
			token: oauth.Token{Expiry: time.Now().Add(time.Hour)},
			want:  false,
		},
		{
			name:  "no expiry",
			token: oauth.Token{Access: "a"},
			want:  true,
		},
		{
			name:  "one hour left",
			token: oauth.Token{Access: "a", Expiry: time.Now().Add(time.Hour)},
			want:  true,
		},
		{
			name:  "less than the margin left",
			token: oauth.Token{Access: "a", Expiry: time.Now().Add(4 * time.Minute)},
			want:  false,
		},
		{
			name:  "expired",
			token: oauth.Token{Access: "a", Expiry: time.Now().Add(-time.Minute)},
			want:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.token.Valid())
		})
	}
}

func TestNewPKCE(t *testing.T) {
	p := oauth.NewPKCE()

	// RFC 7636 requires a verifier of 43 to 128 characters.
	assert.Len(t, p.Verifier, 43)
	sum := sha256.Sum256([]byte(p.Verifier))
	assert.Equal(t, base64.RawURLEncoding.EncodeToString(sum[:]), p.Challenge)
	assert.NotEqual(t, p.Verifier, oauth.NewPKCE().Verifier)
}
