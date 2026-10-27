package oauth_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai/oauth"
)

// sourceFunc adapts a function to [oauth.Source].
type sourceFunc func(ctx context.Context) (oauth.Token, error)

func (f sourceFunc) Token(ctx context.Context) (oauth.Token, error) {
	return f(ctx)
}

func TestTransport_SetsBearer(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
	}))
	defer srv.Close()

	src := sourceFunc(func(context.Context) (oauth.Token, error) {
		return oauth.Token{Access: "abc"}, nil
	})
	client := &http.Client{Transport: &oauth.Transport{Source: src}}

	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer old")
	res, err := client.Do(req)
	require.NoError(t, err)
	res.Body.Close()

	assert.Equal(t, "Bearer abc", got)
	assert.Equal(t, "Bearer old", req.Header.Get("Authorization"), "the transport must not change the request of the caller")
}

func TestTransport_SourceError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the transport sent a request without a token")
	}))
	defer srv.Close()

	errNoToken := errors.New("no token")
	src := sourceFunc(func(context.Context) (oauth.Token, error) {
		return oauth.Token{}, errNoToken
	})
	body := &closeRecorder{Reader: strings.NewReader("{}")}
	req, err := http.NewRequest(http.MethodPost, srv.URL, body)
	require.NoError(t, err)

	_, err = (&oauth.Transport{Source: src}).RoundTrip(req)

	require.ErrorIs(t, err, errNoToken)
	assert.True(t, body.closed, "RoundTrip must close the body on an error")
}

// closeRecorder is a request body that records a call to Close.
type closeRecorder struct {
	io.Reader
	closed bool
}

func (c *closeRecorder) Close() error {
	c.closed = true
	return nil
}
