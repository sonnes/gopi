package higgsfield

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

// Error is a failed answer of the API. Read it with errors.As.
type Error struct {
	// StatusCode is the HTTP status of the answer.
	StatusCode int
	// Header is the header of the answer, such as Retry-After.
	Header http.Header
	// Body is the start of the body of the answer, up to 1024 bytes.
	Body string
}

// Error returns the status and the body of the answer.
func (e *Error) Error() string {
	return fmt.Sprintf("status %d: %s", e.StatusCode, e.Body)
}

// call sends one request of the API through [retry.Call], so each call
// retries and names the provider in its error in the same way. build
// makes a new request for each try, because a try reads the body.
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
		p.authorize(req, opts)
		return p.do(req)
	}
	return retry.Call(ctx, opts, providerName, statusOf, send)
}

// authorize sets the key and the headers of opts on req. A header of
// [ai.Headers] replaces a header of the provider with the same name.
func (p *Provider) authorize(req *http.Request, opts ai.Options) {
	req.Header.Set("Authorization", keyScheme+p.cfg.apiKey)
	headers, _ := ai.GetOption(opts, ai.Headers)
	for name, value := range headers {
		req.Header.Set(name, value)
	}
}

// do sends req and returns the body of an answer with a 2xx status.
func (p *Provider) do(req *http.Request) ([]byte, error) {
	resp, err := p.cfg.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	succeeded := resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices
	if !succeeded {
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
