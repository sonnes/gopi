package gemini

import (
	"context"
	"errors"

	"google.golang.org/genai/interactions/models/interactions"
	"google.golang.org/genai/interactions/models/operations"
	"google.golang.org/genai/interactions/retry"

	gopiretry "github.com/sonnes/gopi/internal/retry"
	"github.com/sonnes/gopi/pkg/ai"
)

const providerName = "gemini"

// noRetry stops the retries of the Interactions client. By default it
// retries 4 times, and gopi retries only before the first event, with
// the policy of [ai.MaxRetries].
var noRetry = operations.WithRetries(retry.Config{Strategy: "none"})

// call sends one request through [gopiretry.Call], so each call of the
// provider retries, marks a refused size, and names the provider in its
// error.
func call[T any](ctx context.Context, opts ai.Options, send func() (T, error)) (T, error) {
	return gopiretry.Call(ctx, opts, providerName, retryStatus, send)
}

// The SDK sends the request before it returns a stream, so a retry
// happens before the first event.
func (p *Provider) interact(
	ctx context.Context,
	body interactions.CreateModelInteraction,
	opts ai.Options,
) (*operations.CreateInteractionResponse, error) {
	req := operations.CreateInteractionRequest{
		Body: operations.NewCreateInteractionRequestBody(body),
	}
	reqOpts := requestOptions(opts)

	send := func() (*operations.CreateInteractionResponse, error) {
		res, err := p.client.Interactions.Create(ctx, req, reqOpts...)
		return res, apiError(err)
	}
	return call(ctx, opts, send)
}

func requestOptions(opts ai.Options) []operations.Option {
	reqOpts := []operations.Option{noRetry}
	if headers, ok := ai.GetOption(opts, ai.Headers); ok {
		reqOpts = append(reqOpts, operations.WithSetHeaders(headers))
	}
	return reqOpts
}

func retryStatus(err error) (gopiretry.Status, bool) {
	var e *Error
	if !errors.As(err, &e) {
		return gopiretry.Status{}, false
	}
	return gopiretry.Status{
		Code:   e.StatusCode,
		Header: e.Header,
	}, true
}
