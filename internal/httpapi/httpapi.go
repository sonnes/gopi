// Package httpapi sends the plain HTTP requests of a provider that its
// SDK does not send, such as the model list of OpenRouter, Fireworks, or
// the ChatGPT plan, and the decisions of OpenRouter and Gemini. A
// response with a status other than 200 is a [StatusError], so a caller
// reads the status with errors.As.
package httpapi

import (
	"bytes"
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
	// Header holds the headers of the response, such as Retry-After.
	Header http.Header
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
	return do(client, req, key)
}

// Post sends body as JSON to url with client, and returns the body of a
// response with status 200. The request also sends header. The key and
// the client work as in [Get].
func Post(
	ctx context.Context,
	client *http.Client,
	url string,
	key string,
	body []byte,
	header http.Header,
) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for name, values := range header {
		for _, v := range values {
			req.Header.Add(name, v)
		}
	}
	req.Header.Set("Content-Type", "application/json")
	return do(client, req, key)
}

func do(client *http.Client, req *http.Request, key string) ([]byte, error) {
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
			Header: resp.Header,
			Body:   string(body),
		}
	}
	return io.ReadAll(resp.Body)
}
