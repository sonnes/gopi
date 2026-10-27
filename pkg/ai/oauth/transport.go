package oauth

import (
	"fmt"
	"net/http"
)

// Transport is an [http.RoundTripper] that sets the header
// "Authorization: Bearer <access token>" on each request. It gets the
// token from Source before each request, so a refresh in the source
// applies to the next request.
type Transport struct {
	// Source gives the token. It is required.
	Source Source
	// Base sends the request. If it is nil, the transport uses
	// [http.DefaultTransport].
	Base http.RoundTripper
}

// RoundTrip implements [http.RoundTripper]. It sends a copy of req, so
// the request of the caller does not change.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	token, err := t.Source.Token(req.Context())
	if err != nil {
		if req.Body != nil {
			req.Body.Close()
		}
		return nil, fmt.Errorf("oauth: get a token: %w", err)
	}

	out := req.Clone(req.Context())
	out.Header.Set("Authorization", "Bearer "+token.Access)

	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(out)
}
