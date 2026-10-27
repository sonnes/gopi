package oauth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// loopbackHost is the host of the listener and of the redirect URI.
const loopbackHost = "127.0.0.1"

// Callback is the loopback listener of an authorization code flow. It
// receives the redirect of the browser. Open it with [Listen] before
// the browser opens the authorize URL, and close it after
// [Callback.Wait] returns.
type Callback struct {
	srv       *http.Server
	uri       string
	redirects chan redirect
}

// redirect is one request of the browser. Wait sends the page for the
// browser on reply.
type redirect struct {
	query url.Values
	reply chan result
}

// result is the page that the browser gets for a redirect.
type result struct {
	status  int
	title   string
	message string
}

var (
	resultOK = result{
		status:  http.StatusOK,
		title:   "Signed in",
		message: "You can close this window.",
	}
	resultFailed = result{
		status:  http.StatusBadRequest,
		title:   "Sign-in failed",
		message: "Read the terminal for the error.",
	}
	resultForeign = result{
		status:  http.StatusBadRequest,
		title:   "Sign-in failed",
		message: "The redirect is not for this sign-in.",
	}
)

// Listen binds 127.0.0.1:port and serves path. If port is 0, the system
// picks a free port, and [Callback.RedirectURI] reports it.
func Listen(port int, path string) (*Callback, error) {
	addr := net.JoinHostPort(loopbackHost, strconv.Itoa(port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("oauth: listen on %s: %w", addr, err)
	}

	bound := ln.Addr().(*net.TCPAddr).Port
	c := &Callback{
		uri:       "http://" + net.JoinHostPort(loopbackHost, strconv.Itoa(bound)) + path,
		redirects: make(chan redirect),
	}
	mux := http.NewServeMux()
	mux.HandleFunc(path, c.serve)
	c.srv = &http.Server{Handler: mux}
	go c.srv.Serve(ln)
	return c, nil
}

// RedirectURI returns "http://127.0.0.1:<port><path>". The authorize
// request and the code exchange send this value.
func (c *Callback) RedirectURI() string {
	return c.uri
}

// Close stops the listener.
func (c *Callback) Close() error {
	return c.srv.Close()
}

// Wait returns the query of the first redirect that carries state. It
// ignores a redirect with another state. The browser gets its page
// after Wait reads the redirect, so a redirect that comes before Wait
// runs waits for it. If that redirect carries an
// error parameter, Wait returns an [*Error].
//
// If paste is not nil, Wait also calls it, for a host where the browser
// cannot reach the listener, such as a host over SSH. paste returns the
// redirect URL, a "code#state" value, or a bare code. The first valid
// answer from the browser or from paste wins. If paste returns an
// error, Wait waits for the browser only. Wait cancels the context of
// paste when it returns.
//
// Wait returns the full query, so a caller reads the provider fields of
// the redirect, such as an issued client_id.
func (c *Callback) Wait(
	ctx context.Context,
	state string,
	paste func(context.Context) (string, error),
) (url.Values, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	pasted := make(chan url.Values, 1)
	if paste != nil {
		go func() {
			input, err := paste(ctx)
			if err != nil {
				return
			}
			pasted <- parsePasted(input, state)
		}()
	}

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case q := <-pasted:
			return checkPasted(q, state)
		case r := <-c.redirects:
			if r.query.Get("state") != state {
				r.reply <- resultForeign
				continue
			}
			q, err := checkRedirect(r.query)
			if err != nil {
				r.reply <- resultFailed
				return nil, err
			}
			r.reply <- resultOK
			return q, nil
		}
	}
}

// serve gives one redirect to Wait, and it shows the page that Wait
// sends back. If the browser leaves first, serve stops.
func (c *Callback) serve(w http.ResponseWriter, r *http.Request) {
	rd := redirect{
		query: r.URL.Query(),
		reply: make(chan result, 1),
	}
	select {
	case c.redirects <- rd:
	case <-r.Context().Done():
		return
	}

	var res result
	select {
	case res = <-rd.reply:
	case <-r.Context().Done():
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(res.status)
	fmt.Fprint(w, page(res.title, res.message))
}

// checkRedirect returns the query of a redirect with the correct state,
// or the error that it carries.
func checkRedirect(q url.Values) (url.Values, error) {
	if code := q.Get("error"); code != "" {
		return nil, &Error{
			Code:        code,
			Description: q.Get("error_description"),
		}
	}
	if q.Get("code") == "" {
		return nil, errors.New("oauth: the redirect has no code")
	}
	return q, nil
}

// checkPasted returns the query of a pasted value.
func checkPasted(q url.Values, state string) (url.Values, error) {
	if q.Get("code") == "" && q.Get("error") == "" {
		return nil, errors.New("oauth: the pasted value has no code")
	}
	pastedState := q.Get("state")
	if pastedState != "" && pastedState != state {
		return nil, errors.New("oauth: the pasted value is for another sign-in")
	}
	return checkRedirect(q)
}

// parsePasted reads a redirect URL, a "code#state" value, or a bare
// code. A bare code gets the state of the sign-in, because it carries
// none.
func parsePasted(input, state string) url.Values {
	input = strings.TrimSpace(input)
	if input == "" {
		return url.Values{}
	}
	u, err := url.Parse(input)
	if err == nil && (u.Query().Has("code") || u.Query().Has("error")) {
		return u.Query()
	}
	if code, pastedState, ok := strings.Cut(input, "#"); ok {
		return url.Values{
			"code":  {code},
			"state": {pastedState},
		}
	}
	return url.Values{
		"code":  {input},
		"state": {state},
	}
}

// page is the HTML page that the browser shows after the redirect.
func page(title, message string) string {
	return `<!doctype html><meta charset="utf-8"><title>` + title + `</title>` +
		`<body style="font-family:system-ui;display:grid;place-items:center;height:90%">` +
		`<div><h1>` + title + `</h1><p>` + message + `</p></div></body>`
}
