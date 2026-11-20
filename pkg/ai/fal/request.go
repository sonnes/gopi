package fal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/sonnes/gopi/internal/retry"
	"github.com/sonnes/gopi/pkg/ai"
)

// maxErrorBody is the most bytes of an error body that an [*Error]
// holds.
const maxErrorBody = 1024

// Error is a failed answer of the API. Read it with errors.As. The body
// of a failed sync call is JSON with the fields "detail" and
// "error_type".
type Error struct {
	// StatusCode is the HTTP status of the answer.
	StatusCode int
	// Header is the header of the answer, such as X-Fal-Needs-Retry.
	Header http.Header
	// Body is the start of the body of the answer, up to 1024 bytes.
	Body string
}

// Error returns the status and the body of the answer.
func (e *Error) Error() string {
	return fmt.Sprintf("status %d: %s", e.StatusCode, e.Body)
}

// call sends one request through [retry.Call], so each call retries
// and names the provider in its error in the same way. build makes a
// new request for each try, because a try reads the body.
func (p *Provider) call(
	ctx context.Context,
	opts ai.Options,
	build func() (*http.Request, error),
) ([]byte, error) {
	send := func() ([]byte, error) {
		req, err := build()
		if err != nil {
			return nil, err
		}
		return p.do(req, opts)
	}
	return retry.Call(ctx, opts, providerName, statusOf, send)
}

// do sends req with the key and the headers of opts, and returns the
// body of an answer with status 200. A header of [ai.Headers] replaces
// a header of the provider with the same name.
func (p *Provider) do(req *http.Request, opts ai.Options) ([]byte, error) {
	req.Header.Set("Authorization", keyScheme+p.cfg.apiKey)
	if headers, ok := ai.GetOption(opts, ai.Headers); ok {
		for name, value := range headers {
			req.Header.Set(name, value)
		}
	}

	resp, err := p.cfg.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return nil, &Error{
			StatusCode: resp.StatusCode,
			Header:     resp.Header,
			Body:       string(body),
		}
	}
	return io.ReadAll(resp.Body)
}

// statusOf reads the status and the headers of a failed answer.
func statusOf(err error) (retry.Status, bool) {
	var e *Error
	if !errors.As(err, &e) {
		return retry.Status{}, false
	}
	return retry.Status{Code: e.StatusCode, Header: e.Header}, true
}
