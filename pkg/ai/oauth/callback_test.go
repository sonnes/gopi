package oauth_test

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai/oauth"
)

const callbackPath = "/auth/callback"

// listen opens a callback on a free port and closes it at the end of the
// test.
func listen(t *testing.T) *oauth.Callback {
	t.Helper()
	cb, err := oauth.Listen(0, callbackPath)
	require.NoError(t, err)
	t.Cleanup(func() { cb.Close() })
	return cb
}

// redirect sends the browser redirect with the query q to cb, as a
// browser does, and returns the status of the page that the browser
// gets. The page comes after Wait reads the redirect.
func redirect(t *testing.T, cb *oauth.Callback, q url.Values) <-chan int {
	t.Helper()
	status := make(chan int, 1)
	go func() {
		res, err := http.Get(cb.RedirectURI() + "?" + q.Encode())
		if err != nil {
			t.Error(err)
			status <- 0
			return
		}
		res.Body.Close()
		status <- res.StatusCode
	}()
	return status
}

func TestListen_RedirectURI(t *testing.T) {
	cb := listen(t)

	u, err := url.Parse(cb.RedirectURI())
	require.NoError(t, err)
	assert.Equal(t, "http", u.Scheme)
	assert.Equal(t, "127.0.0.1", u.Hostname())
	assert.NotEqual(t, "0", u.Port())
	assert.Equal(t, callbackPath, u.Path)
}

func TestCallback_Wait(t *testing.T) {
	cb := listen(t)

	status := redirect(t, cb, url.Values{
		"code":      {"the-code"},
		"state":     {"s1"},
		"client_id": {"oaiapp_1"},
	})
	q, err := cb.Wait(t.Context(), "s1", nil)

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, <-status)
	assert.Equal(t, "the-code", q.Get("code"))
	assert.Equal(t, "oaiapp_1", q.Get("client_id"))
}

func TestCallback_WaitIgnoresOtherState(t *testing.T) {
	cb := listen(t)

	type waited struct {
		q   url.Values
		err error
	}
	done := make(chan waited, 1)
	go func() {
		q, err := cb.Wait(t.Context(), "s1", nil)
		done <- waited{q, err}
	}()
	forged := <-redirect(t, cb, url.Values{"code": {"forged"}, "state": {"other"}})
	real := <-redirect(t, cb, url.Values{"code": {"the-code"}, "state": {"s1"}})
	w := <-done

	require.NoError(t, w.err)
	assert.Equal(t, http.StatusBadRequest, forged)
	assert.Equal(t, http.StatusOK, real)
	assert.Equal(t, "the-code", w.q.Get("code"))
}

func TestCallback_WaitProviderError(t *testing.T) {
	cb := listen(t)

	status := redirect(t, cb, url.Values{
		"error":             {"access_denied"},
		"error_description": {"The user said no."},
		"state":             {"s1"},
	})
	_, err := cb.Wait(t.Context(), "s1", nil)

	assert.Equal(t, http.StatusBadRequest, <-status)
	var oerr *oauth.Error
	require.ErrorAs(t, err, &oerr)
	assert.Equal(t, "access_denied", oerr.Code)
	assert.Equal(t, "The user said no.", oerr.Description)
}

func TestCallback_WaitPaste(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantCode string
		wantErr  bool
	}{
		{
			name:     "redirect URL",
			input:    "http://127.0.0.1:1455/auth/callback?code=c1&state=s1&client_id=oaiapp_1",
			wantCode: "c1",
		},
		{
			name:     "code and state",
			input:    "  c2#s1\n",
			wantCode: "c2",
		},
		{
			name:     "bare code",
			input:    "c3",
			wantCode: "c3",
		},
		{
			name:    "other state",
			input:   "http://127.0.0.1:1455/auth/callback?code=c4&state=other",
			wantErr: true,
		},
		{
			name:    "empty",
			input:   "   ",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cb := listen(t)
			paste := func(context.Context) (string, error) {
				return tt.input, nil
			}

			q, err := cb.Wait(t.Context(), "s1", paste)

			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantCode, q.Get("code"))
		})
	}
}

func TestCallback_WaitPasteEndsWithoutInput(t *testing.T) {
	cb := listen(t)
	paste := func(context.Context) (string, error) {
		return "", io.EOF
	}

	status := redirect(t, cb, url.Values{"code": {"the-code"}, "state": {"s1"}})
	q, err := cb.Wait(t.Context(), "s1", paste)

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, <-status)
	assert.Equal(t, "the-code", q.Get("code"))
}

func TestCallback_WaitCanceled(t *testing.T) {
	cb := listen(t)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	_, err := cb.Wait(ctx, "s1", nil)

	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestCallback_PageForTheBrowser(t *testing.T) {
	cb := listen(t)

	type page struct {
		contentType string
		body        string
	}
	got := make(chan page, 1)
	go func() {
		res, err := http.Get(cb.RedirectURI() + "?code=c&state=s1")
		if err != nil {
			t.Error(err)
			got <- page{}
			return
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		got <- page{res.Header.Get("Content-Type"), string(body)}
	}()
	_, err := cb.Wait(t.Context(), "s1", nil)
	require.NoError(t, err)

	p := <-got
	assert.Contains(t, p.contentType, "text/html")
	assert.True(t, strings.Contains(p.body, "close this window"))
}
