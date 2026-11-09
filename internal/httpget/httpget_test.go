package httpget

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGet(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)

	body, err := Get(t.Context(), nil, srv.URL, "sk-test")
	require.NoError(t, err)
	assert.JSONEq(t, `{"ok":true}`, string(body))
	assert.Equal(t, "Bearer sk-test", auth)
}

func TestGet_NoKey(t *testing.T) {
	var auth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Values("Authorization")
	}))
	t.Cleanup(srv.Close)

	_, err := Get(t.Context(), srv.Client(), srv.URL, "")
	require.NoError(t, err)
	assert.Empty(t, auth)
}

func TestGet_StatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(strings.Repeat("x", 2*maxErrorBody)))
	}))
	t.Cleanup(srv.Close)

	_, err := Get(t.Context(), nil, srv.URL, "bad")

	var status *StatusError
	require.ErrorAs(t, err, &status)
	assert.Equal(t, http.StatusUnauthorized, status.Code)
	assert.Len(t, status.Body, maxErrorBody)
	assert.ErrorContains(t, err, "401 Unauthorized: xxx")
}
