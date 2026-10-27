package chatgpt

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"time"

	"github.com/sonnes/gopi/pkg/ai/oauth"
)

// Account is one ChatGPT account, signed in on one host.
type Account struct {
	// ClientID is the client ID that OpenAI issued at the first sign-in,
	// such as "oaiapp_...". It is never "dynamic_agent_client".
	ClientID string
	// HostID identifies the host, such as "urn:uuid:...". It stays the
	// same for each sign-in on the host. It is not a credential.
	HostID string
	// Token holds the OAuth tokens and the granted scopes.
	Token oauth.Token
	// IDToken is the ID token of the last sign-in. The next sign-in
	// sends it as id_token_hint.
	IDToken string
	// Subject is the "sub" claim of the ID token: the ID of the account.
	Subject string
	// Email is the email of the account. The next sign-in sends it as
	// login_hint.
	Email string
}

// SignedIn reports whether the account has a token.
func (a Account) SignedIn() bool {
	return a.Token.Access != "" || a.Token.Refresh != ""
}

// Registration returns the account with its client ID and its host ID
// only. A caller keeps it after a sign-out, so the next sign-in uses the
// same registration.
func (a Account) Registration() Account {
	return Account{
		ClientID: a.ClientID,
		HostID:   a.HostID,
	}
}

// accountJSON is the JSON form of an [Account].
type accountJSON struct {
	ClientID     string    `json:"client_id"`
	HostID       string    `json:"host_id"`
	AccessToken  string    `json:"access_token,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	Expiry       time.Time `json:"expiry,omitzero"`
	Scopes       []string  `json:"scopes,omitempty"`
	IDToken      string    `json:"id_token,omitempty"`
	Subject      string    `json:"subject,omitempty"`
	Email        string    `json:"email,omitempty"`
}

// MarshalJSON implements [json.Marshaler].
func (a Account) MarshalJSON() ([]byte, error) {
	return json.Marshal(accountJSON{
		ClientID:     a.ClientID,
		HostID:       a.HostID,
		AccessToken:  a.Token.Access,
		RefreshToken: a.Token.Refresh,
		Expiry:       a.Token.Expiry,
		Scopes:       a.Token.Scopes,
		IDToken:      a.IDToken,
		Subject:      a.Subject,
		Email:        a.Email,
	})
}

// UnmarshalJSON implements [json.Unmarshaler].
func (a *Account) UnmarshalJSON(data []byte) error {
	var j accountJSON
	if err := json.Unmarshal(data, &j); err != nil {
		return err
	}
	*a = Account{
		ClientID: j.ClientID,
		HostID:   j.HostID,
		Token: oauth.Token{
			Access:  j.AccessToken,
			Refresh: j.RefreshToken,
			Expiry:  j.Expiry,
			Scopes:  j.Scopes,
		},
		IDToken: j.IDToken,
		Subject: j.Subject,
		Email:   j.Email,
	}
	return nil
}

// NewHostID returns a new host ID: a random UUID version 4, as
// "urn:uuid:<uuid>". A caller makes it once for a host and keeps it.
func NewHostID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = b[6]&0x0f | 0x40 // version 4
	b[8] = b[8]&0x3f | 0x80 // the variant of RFC 9562
	return fmt.Sprintf("urn:uuid:%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
