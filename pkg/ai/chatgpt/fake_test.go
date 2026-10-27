package chatgpt_test

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai/chatgpt"
)

// testKeyID is the kid of the signing key of the fake server.
const testKeyID = "test-key"

// issuedClientID is the client ID that the fake server issues on a
// first sign-in.
const issuedClientID = "oaiapp_test"

// fakeOpenAI is a test server for the sign-in, the tokens, the model
// list, and the Responses route.
type fakeOpenAI struct {
	t   *testing.T
	srv *httptest.Server
	key *rsa.PrivateKey

	mu sync.Mutex
	// authorize is the query of the last authorize URL.
	authorize url.Values
	// tokenForms are the forms of the token requests.
	tokenForms []url.Values
	// revokeForms are the forms of the revocation requests.
	revokeForms []url.Values
	// refreshes counts the refresh requests.
	refreshes atomic.Int32
	// scope is the granted scope. It defaults to the full scope.
	scope string
	// nonce, if set, replaces the nonce of the ID token.
	nonce string
	// signWith, if set, signs the ID token with another key.
	signWith *rsa.PrivateKey
	// noClientID drops the issued client ID from the callback.
	noClientID bool
	// refreshError is the error code of each refresh, if set.
	refreshError string
	// responses holds the bodies of the /v1/responses requests.
	responses []map[string]any
	// bearer is the Authorization header of the last API request.
	bearer string
}

func newFakeOpenAI(t *testing.T) *fakeOpenAI {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	f := &fakeOpenAI{
		t:     t,
		key:   key,
		scope: "openid profile email offline_access resource.invoke chatgpt.tokens.use.direct",
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", f.token)
	mux.HandleFunc("/revoke", f.revoke)
	mux.HandleFunc("/jwks", f.jwks)
	mux.HandleFunc("/v1/models", f.models)
	mux.HandleFunc("/v1/responses", f.respond)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// auth returns the endpoints of the fake server.
func (f *fakeOpenAI) auth() chatgpt.Auth {
	return chatgpt.Auth{
		AuthorizeURL: f.srv.URL + "/authorize",
		TokenURL:     f.srv.URL + "/token",
		RevokeURL:    f.srv.URL + "/revoke",
		JWKSURL:      f.srv.URL + "/jwks",
		Issuer:       f.srv.URL,
		Client:       f.srv.Client(),
	}
}

// open acts as the browser: it reads the authorize URL, and it sends
// the redirect with a code to the redirect URI.
func (f *fakeOpenAI) open(authorizeURL string) error {
	u, err := url.Parse(authorizeURL)
	if err != nil {
		return err
	}
	q := u.Query()
	f.mu.Lock()
	f.authorize = q
	noClientID := f.noClientID
	f.mu.Unlock()

	back := url.Values{
		"code":  {"the-code"},
		"state": {q.Get("state")},
		"scope": {q.Get("scope")},
	}
	if q.Get("client_id") == "dynamic_agent_client" && !noClientID {
		back.Set("client_id", issuedClientID)
	}
	go func() {
		res, err := http.Get(q.Get("redirect_uri") + "?" + back.Encode())
		if err == nil {
			res.Body.Close()
		}
	}()
	return nil
}

func (f *fakeOpenAI) token(w http.ResponseWriter, r *http.Request) {
	require.NoError(f.t, r.ParseForm())
	f.mu.Lock()
	f.tokenForms = append(f.tokenForms, r.PostForm)
	authorize := f.authorize
	f.mu.Unlock()

	if r.PostForm.Get("grant_type") == "refresh_token" {
		f.refreshes.Add(1)
		if f.refreshError != "" {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `{"error":%q}`, f.refreshError)
			return
		}
		// A slow refresh widens the window for a race between two
		// sources.
		time.Sleep(50 * time.Millisecond)
		n := f.refreshes.Load()
		writeJSON(w, map[string]any{
			"access_token":  fmt.Sprintf("access-%d", n),
			"refresh_token": fmt.Sprintf("refresh-%d", n),
			"expires_in":    3600,
			"scope":         f.scope,
			"token_type":    "Bearer",
		})
		return
	}

	nonce := authorize.Get("nonce")
	if f.nonce != "" {
		nonce = f.nonce
	}
	writeJSON(w, map[string]any{
		"access_token":  "access-0",
		"refresh_token": "refresh-0",
		"id_token":      f.idToken(r.PostForm.Get("client_id"), nonce),
		"expires_in":    3600,
		"scope":         f.scope,
		"token_type":    "Bearer",
	})
}

// idToken returns an RS256 ID token for the client ID and the nonce.
func (f *fakeOpenAI) idToken(clientID, nonce string) string {
	header := `{"alg":"RS256","kid":"` + testKeyID + `","typ":"JWT"}`
	claims, err := json.Marshal(map[string]any{
		"iss":   f.srv.URL,
		"aud":   []string{clientID},
		"sub":   "user-1",
		"email": "you@example.com",
		"nonce": nonce,
		"exp":   time.Now().Add(time.Hour).Unix(),
		"iat":   time.Now().Unix(),
	})
	require.NoError(f.t, err)

	enc := base64.RawURLEncoding
	signed := enc.EncodeToString([]byte(header)) + "." + enc.EncodeToString(claims)
	key := f.key
	if f.signWith != nil {
		key = f.signWith
	}
	sum := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	require.NoError(f.t, err)
	return signed + "." + enc.EncodeToString(sig)
}

func (f *fakeOpenAI) jwks(w http.ResponseWriter, _ *http.Request) {
	enc := base64.RawURLEncoding
	pub := f.key.PublicKey
	writeJSON(w, map[string]any{"keys": []any{map[string]any{
		"kty": "RSA",
		"kid": testKeyID,
		"alg": "RS256",
		"use": "sig",
		"n":   enc.EncodeToString(pub.N.Bytes()),
		"e":   enc.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}}})
}

func (f *fakeOpenAI) revoke(w http.ResponseWriter, r *http.Request) {
	require.NoError(f.t, r.ParseForm())
	f.mu.Lock()
	f.revokeForms = append(f.revokeForms, r.PostForm)
	f.mu.Unlock()
}

// modelsBody is a trimmed answer of GET /v1/models of a live account.
const modelsBody = `{"models":[
{"slug":"gpt-6.1-sol","display_name":"GPT-6.1 Sol","visibility":"list","supported_in_api":true,"context_window":272000,
 "input_modalities":["text","image"],
 "supported_reasoning_levels":[{"effort":"low"},{"effort":"medium"},{"effort":"high"},{"effort":"xhigh"},{"effort":"ultra"}],"priority":1},
{"slug":"gpt-reserve","display_name":"GPT Reserve","visibility":"hide","supported_in_api":true,"context_window":272000,"supported_reasoning_levels":[]},
{"slug":"gpt-6-luna","display_name":"GPT-6 Luna","visibility":"list","supported_in_api":true,"context_window":400000,"supported_reasoning_levels":[]},
{"slug":"codex-only","display_name":"Codex Only","visibility":"list","supported_in_api":false,"context_window":272000,"supported_reasoning_levels":[]}
]}`

func (f *fakeOpenAI) models(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.bearer = r.Header.Get("Authorization")
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, modelsBody)
}

// textEvents is one text answer of the Responses route, with an empty
// output in the terminal event, as the route sends it.
var textEvents = []string{
	`{"type":"response.created","response":{"id":"resp_1","status":"in_progress","output":[]}}`,
	`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg_1","type":"message","role":"assistant","content":[]}}`,
	`{"type":"response.content_part.added","output_index":0,"content_index":0,"part":{"type":"output_text","text":""}}`,
	`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"Hello."}`,
	`{"type":"response.output_item.done","output_index":0,"item":{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Hello.","annotations":[]}]}}`,
	`{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[],"usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7}}}`,
}

func (f *fakeOpenAI) respond(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	require.NoError(f.t, json.NewDecoder(r.Body).Decode(&body))
	f.mu.Lock()
	f.bearer = r.Header.Get("Authorization")
	f.responses = append(f.responses, body)
	f.mu.Unlock()

	w.Header().Set("Content-Type", "text/event-stream")
	for _, ev := range textEvents {
		fmt.Fprintf(w, "data: %s\n\n", ev)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// scopeHas reports whether a space-separated scope has s.
func scopeHas(scope, s string) bool {
	for _, f := range strings.Fields(scope) {
		if f == s {
			return true
		}
	}
	return false
}
