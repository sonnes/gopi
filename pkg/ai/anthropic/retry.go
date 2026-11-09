package anthropic

import (
	"context"
	"errors"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"

	"github.com/sonnes/gopi/internal/retry"
	"github.com/sonnes/gopi/pkg/ai"
)

// call sends one request through [retry.Call], so each call of the
// provider retries, marks a refused size, and names the provider in its
// error.
func call[T any](ctx context.Context, opts ai.Options, send func() (T, error)) (T, error) {
	return retry.Call(ctx, opts, "anthropic", retryStatus, send)
}

// openStream sends the request and returns the stream. The SDK sends
// the request before it returns the stream, so a retry happens before
// the first event.
func (p *Provider) openStream(
	ctx context.Context,
	params sdk.MessageNewParams,
	opts ai.Options,
	reqOpts []option.RequestOption,
) (*ssestream.Stream[sdk.MessageStreamEventUnion], error) {
	send := func() (*ssestream.Stream[sdk.MessageStreamEventUnion], error) {
		stream := p.client.Messages.NewStreaming(ctx, params, reqOpts...)
		return stream, stream.Err()
	}
	return call(ctx, opts, send)
}

// retryStatus reads the status and the headers of an API error.
func retryStatus(err error) (retry.Status, bool) {
	var apiErr *sdk.Error
	if !errors.As(err, &apiErr) || apiErr.Response == nil {
		return retry.Status{}, false
	}
	return retry.Status{
		Code:   apiErr.StatusCode,
		Header: apiErr.Response.Header,
	}, true
}
