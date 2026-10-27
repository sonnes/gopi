// Package oauth holds the parts of an OAuth 2.0 client that do not
// depend on a provider: a token, a source of tokens, a transport that
// adds the token to each request, PKCE, and the loopback listener of an
// authorization code flow.
//
// A caller must obey these rules:
//
//   - A [Source] refreshes its own token. The [Transport] asks the
//     source for a token before each request, and it keeps no token.
//   - A provider package holds the endpoints, the client ID, and the
//     refresh request of its provider. This package has none of them.
//   - The listener binds 127.0.0.1 only. Some providers refuse
//     "localhost" in a redirect URI.
//
// The package does not use golang.org/x/oauth2, because its refresh
// request cannot carry extra parameters, such as the resource
// parameter of RFC 8707.
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"time"
)

// expiryMargin is the time before the expiry of a token when the token
// stops being valid. A request that starts inside the margin then does
// not reach the server with an expired token.
const expiryMargin = 5 * time.Minute

// verifierBytes is the number of random bytes in a PKCE verifier. The
// base64url form of 32 bytes has 43 characters, the minimum of RFC 7636.
const verifierBytes = 32

// Token is an OAuth access token and the data to refresh it.
type Token struct {
	// Access is the access token.
	Access string
	// Refresh is the refresh token. It is empty if the server gave none.
	Refresh string
	// Expiry is the time when the access token expires. The zero time
	// means that the expiry is unknown.
	Expiry time.Time
	// Scopes are the scopes that the server granted.
	Scopes []string
}

// Valid reports whether the token has an access token that expires in
// more than 5 minutes. A token with an unknown expiry is valid.
func (t Token) Valid() bool {
	if t.Access == "" {
		return false
	}
	if t.Expiry.IsZero() {
		return true
	}
	return time.Until(t.Expiry) > expiryMargin
}

// Source gives a valid token. If its token is not valid, a source
// refreshes it before it returns.
type Source interface {
	Token(ctx context.Context) (Token, error)
}

// PKCE is a verifier and its S256 challenge, as RFC 7636 defines them.
// The authorize request sends the challenge, and the code exchange
// sends the verifier.
type PKCE struct {
	Verifier  string
	Challenge string
}

// NewPKCE returns a new random verifier and its challenge.
func NewPKCE() PKCE {
	b := make([]byte, verifierBytes)
	rand.Read(b)
	verifier := base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(verifier))
	return PKCE{
		Verifier:  verifier,
		Challenge: base64.RawURLEncoding.EncodeToString(sum[:]),
	}
}

// Error is an error that the authorization server sent to the redirect
// URI, such as "access_denied".
type Error struct {
	// Code is the error parameter.
	Code string
	// Description is the error_description parameter. It can be empty.
	Description string
}

func (e *Error) Error() string {
	if e.Description == "" {
		return "oauth: " + e.Code
	}
	return "oauth: " + e.Code + ": " + e.Description
}
