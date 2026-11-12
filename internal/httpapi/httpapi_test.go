package httpapi

import (
	"io"
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

func TestPost(t *testing.T) {
	var (
		method      string
		auth        string
		contentType string
		team        string
		got         []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		auth = r.Header.Get("Authorization")
		contentType = r.Header.Get("Content-Type")
		team = r.Header.Get("X-Team")
		got, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)

	body, err := Post(
		t.Context(),
		nil,
		srv.URL,
		"sk-test",
		[]byte(`{"q":1}`),
		http.Header{"X-Team": {"infra"}},
	)

	require.NoError(t, err)
	assert.JSONEq(t, `{"ok":true}`, string(body))
	assert.Equal(t, http.MethodPost, method)
	assert.Equal(t, "Bearer sk-test", auth)
	assert.Equal(t, "application/json", contentType)
	assert.Equal(t, "infra", team)
	assert.JSONEq(t, `{"q":1}`, string(got))
}

func TestPost_StatusErrorKeepsHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)

	_, err := Post(t.Context(), nil, srv.URL, "k", []byte(`{}`), nil)

	var status *StatusError
	require.ErrorAs(t, err, &status)
	assert.Equal(t, http.StatusTooManyRequests, status.Code)
	assert.Equal(t, "2", status.Header.Get("Retry-After"))
}
