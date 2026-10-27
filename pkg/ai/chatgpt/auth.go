package chatgpt

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/sonnes/gopi/pkg/ai/oauth"
)

// The endpoints of Sign in with ChatGPT.
const (
	DefaultAuthorizeURL = "https://auth.openai.com/api/accounts/authorize"
	DefaultTokenURL     = "https://auth.openai.com/api/accounts/oauth/token"
	DefaultRevokeURL    = "https://auth.openai.com/api/accounts/oauth/revoke"
	DefaultJWKSURL      = "https://auth.openai.com/.well-known/jwks.json"
	DefaultIssuer       = "https://auth.openai.com"
)

// DefaultPort is the port of the loopback redirect URI. Only the port
// of the redirect URI can change between sign-ins, so a caller keeps
// one port.
const DefaultPort = 1455

// callbackPath is the path of the loopback redirect URI.
const callbackPath = "/auth/callback"

// resource is the resource parameter of RFC 8707. Each authorize, code,
// and refresh request sends it.
const resource = "https://api.openai.com/v1"

// dynamicClientID is the client ID of a first sign-in. OpenAI then
// issues a client ID for the account and the workspace.
const dynamicClientID = "dynamic_agent_client"

// planScope is the scope that lets the token use the plan. A sign-in
// without it gives only an identity.
const planScope = "chatgpt.tokens.use.direct"

// scopes are the identity scopes and the plan scopes of a sign-in.
var scopes = []string{
	"openid",
	"profile",
	"email",
	"offline_access",
	"resource.invoke",
	planScope,
}

var (
	// ErrSignInExpired means that OpenAI refused the refresh token. The
	// user must sign in again.
	ErrSignInExpired = errors.New("chatgpt: the sign-in expired")
	// ErrNoAccount means that no account is signed in.
	ErrNoAccount = errors.New("chatgpt: no account is signed in")
)

// Auth holds the endpoints of Sign in with ChatGPT. The zero value uses
// the endpoints of OpenAI and [http.DefaultClient]. A test sets the
// fields to a test server.
type Auth struct {
	AuthorizeURL string
	TokenURL     string
	RevokeURL    string
	JWKSURL      string
	// Issuer is the "iss" claim that an ID token must have.
	Issuer string
	// Client sends the requests of the sign-in, the refresh, and the
	// sign-out.
	Client *http.Client
}

func (a Auth) authorizeURL() string { return cmp.Or(a.AuthorizeURL, DefaultAuthorizeURL) }
func (a Auth) tokenURL() string     { return cmp.Or(a.TokenURL, DefaultTokenURL) }
func (a Auth) revokeURL() string    { return cmp.Or(a.RevokeURL, DefaultRevokeURL) }
func (a Auth) jwksURL() string      { return cmp.Or(a.JWKSURL, DefaultJWKSURL) }
func (a Auth) issuer() string       { return cmp.Or(a.Issuer, DefaultIssuer) }

func (a Auth) client() *http.Client {
	if a.Client == nil {
		return http.DefaultClient
	}
	return a.Client
}

// SignInConfig configures [Auth.SignIn].
type SignInConfig struct {
	// AppName is the name that the consent page shows on a first
	// sign-in, such as "gopi".
	AppName string
	// HostID identifies the host on a first sign-in. Make it once with
	// [NewHostID] and keep it. A prior account gives its own host ID.
	HostID string
	// Port is the port of the loopback listener. If it is 0, the system
	// picks a free port. A program uses [DefaultPort].
	Port int
	// Open shows the authorize URL to the user, such as in a browser. It
	// is required.
	Open func(url string) error
	// Paste reads a redirect URL that the user pastes, for a host where
	// the browser cannot reach the listener. It can be nil.
	Paste func(ctx context.Context) (string, error)
}

// SignIn runs the authorization code flow with PKCE, and returns the
// signed-in account.
//
// If prior is nil or has no client ID, SignIn registers the app with
// "dynamic_agent_client" and keeps the client ID that OpenAI issues. If
// prior has a client ID, SignIn uses it and sends the hints of prior.
//
// SignIn accepts the result only if the ID token has a valid RS256
// signature from the keys of OpenAI, the issuer, the issued client ID
// as its audience, a time before its expiry, and the nonce of the
// sign-in. The granted scopes must include the plan scope.
func (a Auth) SignIn(ctx context.Context, cfg SignInConfig, prior *Account) (Account, error) {
	acc := Account{
		ClientID: dynamicClientID,
		HostID:   cfg.HostID,
	}
	if prior != nil {
		acc.ClientID = cmp.Or(prior.ClientID, acc.ClientID)
		acc.HostID = cmp.Or(prior.HostID, acc.HostID)
	}
	if acc.HostID == "" {
		return Account{}, errors.New("chatgpt: sign in: no host ID")
	}
	first := acc.ClientID == dynamicClientID

	cb, err := oauth.Listen(cfg.Port, callbackPath)
	if err != nil {
		return Account{}, fmt.Errorf("chatgpt: sign in: %w", err)
	}
	defer cb.Close()

	pkce := oauth.NewPKCE()
	state := rand.Text()
	nonce := rand.Text()
	q := url.Values{
		"client_id":             {acc.ClientID},
		"ext_agent_host_id":     {acc.HostID},
		"response_type":         {"code"},
		"redirect_uri":          {cb.RedirectURI()},
		"scope":                 {strings.Join(scopes, " ")},
		"resource":              {resource},
		"state":                 {state},
		"nonce":                 {nonce},
		"code_challenge":        {pkce.Challenge},
		"code_challenge_method": {"S256"},
	}
	if first {
		q.Set("agent_name_hint", cfg.AppName)
	}
	if prior != nil && !first {
		if prior.IDToken != "" {
			q.Set("id_token_hint", prior.IDToken)
		}
		if prior.Email != "" {
			q.Set("login_hint", prior.Email)
		}
	}
	if err := cfg.Open(a.authorizeURL() + "?" + q.Encode()); err != nil {
		return Account{}, fmt.Errorf("chatgpt: sign in: open the authorize URL: %w", err)
	}

	back, err := cb.Wait(ctx, state, cfg.Paste)
	if err != nil {
		return Account{}, fmt.Errorf("chatgpt: sign in: %w", err)
	}
	issued := back.Get("client_id")
	switch {
	case first && issued == "":
		return Account{}, errors.New("chatgpt: sign in: OpenAI issued no client_id, so the registration is incomplete")
	case first:
		acc.ClientID = issued
	case issued != "" && issued != acc.ClientID:
		return Account{}, errors.New("chatgpt: sign in: OpenAI returned another client_id")
	}

	tok, err := a.postToken(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {acc.ClientID},
		"code":          {back.Get("code")},
		"code_verifier": {pkce.Verifier},
		"redirect_uri":  {cb.RedirectURI()},
		"resource":      {resource},
	})
	if err != nil {
		return Account{}, fmt.Errorf("chatgpt: sign in: %w", err)
	}
	claims, err := a.verifyIDToken(ctx, tok.IDToken, acc.ClientID, nonce)
	if err != nil {
		return Account{}, fmt.Errorf("chatgpt: sign in: %w", err)
	}
	acc.Token = tok.token(nil)
	if !slices.Contains(acc.Token.Scopes, planScope) {
		return Account{}, fmt.Errorf("chatgpt: sign in: the user did not grant %s", planScope)
	}
	acc.IDToken = tok.IDToken
	acc.Subject = claims.Subject
	acc.Email = claims.Email
	return acc, nil
}

// Refresh exchanges the refresh token of acc, and returns acc with the
// new tokens. The refresh token rotates, so the caller must keep the
// result. If OpenAI refuses the refresh token, the error wraps
// [ErrSignInExpired].
func (a Auth) Refresh(ctx context.Context, acc Account) (Account, error) {
	tok, err := a.postToken(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {acc.ClientID},
		"refresh_token": {acc.Token.Refresh},
		"resource":      {resource},
	})
	if err != nil {
		return Account{}, fmt.Errorf("chatgpt: refresh: %w", err)
	}
	old := acc.Token
	acc.Token = tok.token(old.Scopes)
	acc.Token.Refresh = cmp.Or(acc.Token.Refresh, old.Refresh)
	return acc, nil
}

// SignOut revokes the refresh token of acc. The registration of the app
// stays, so a caller keeps [Account.Registration].
func (a Auth) SignOut(ctx context.Context, acc Account) error {
	form := url.Values{
		"token":           {acc.Token.Refresh},
		"token_type_hint": {"refresh_token"},
		"client_id":       {acc.ClientID},
	}
	status, body, err := a.postForm(ctx, a.revokeURL(), form)
	if err != nil {
		return fmt.Errorf("chatgpt: sign out: %w", err)
	}
	if status != http.StatusOK {
		return fmt.Errorf("chatgpt: sign out: status %d: %s", status, truncate(body))
	}
	return nil
}

// tokenResponse is the body of the token endpoint.
type tokenResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	IDToken          string `json:"id_token"`
	ExpiresIn        int    `json:"expires_in"`
	Scope            string `json:"scope"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// token returns the tokens of r. If r has no scope, the token keeps
// scopes.
func (r tokenResponse) token(scopes []string) oauth.Token {
	t := oauth.Token{
		Access:  r.AccessToken,
		Refresh: r.RefreshToken,
		Scopes:  scopes,
	}
	if r.ExpiresIn > 0 {
		t.Expiry = time.Now().Add(time.Duration(r.ExpiresIn) * time.Second)
	}
	if r.Scope != "" {
		t.Scopes = strings.Fields(r.Scope)
	}
	return t
}

// postToken posts form to the token endpoint.
func (a Auth) postToken(ctx context.Context, form url.Values) (tokenResponse, error) {
	status, body, err := a.postForm(ctx, a.tokenURL(), form)
	if err != nil {
		return tokenResponse{}, err
	}
	var r tokenResponse
	decodeErr := json.Unmarshal(body, &r)
	if status != http.StatusOK {
		if r.Error == "invalid_grant" && form.Get("grant_type") == "refresh_token" {
			return tokenResponse{}, ErrSignInExpired
		}
		if r.Error != "" {
			return tokenResponse{}, &oauth.Error{
				Code:        r.Error,
				Description: r.ErrorDescription,
			}
		}
		return tokenResponse{}, fmt.Errorf("status %d: %s", status, truncate(body))
	}
	if decodeErr != nil {
		return tokenResponse{}, fmt.Errorf("read the token response: %w", decodeErr)
	}
	if r.AccessToken == "" {
		return tokenResponse{}, errors.New("the token response has no access token")
	}
	return r, nil
}

// postForm posts a form-encoded body, and returns the status and the
// body of the response.
func (a Auth) postForm(ctx context.Context, endpoint string, form url.Values) (int, []byte, error) {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		endpoint,
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := a.client().Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return 0, nil, err
	}
	return res.StatusCode, body, nil
}
