// Package httpget sends the GET requests of a provider that its SDK
// does not send, such as the model list of OpenRouter, Fireworks, or the
// ChatGPT plan. A response with a status other than 200 is a
// [StatusError], so a caller reads the status with errors.As.
package httpget

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"net/http"
)

// maxErrorBody is the most bytes of an error body that a [StatusError]
// holds.
const maxErrorBody = 1024

// StatusError is the error of a response with a status other than 200.
type StatusError struct {
	// Code is the HTTP status code, such as 401.
	Code int
	// Status is the status line, such as "401 Unauthorized".
	Status string
	// Body is the start of the body of the response, up to 1024 bytes.
	Body string
}

// Error returns the status line and the body.
func (e *StatusError) Error() string {
	return fmt.Sprintf("%s: %s", e.Status, e.Body)
}

// Get sends a GET request for url with client and returns the body of a
// response with status 200. If key is not empty, the request sends it
// as a bearer token. If client is nil, Get uses [http.DefaultClient].
func Get(ctx context.Context, client *http.Client, url, key string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	resp, err := cmp.Or(client, http.DefaultClient).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return nil, &StatusError{
			Code:   resp.StatusCode,
			Status: resp.Status,
			Body:   string(body),
		}
	}
	return io.ReadAll(resp.Body)
}
