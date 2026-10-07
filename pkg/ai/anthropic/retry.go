package anthropic

import (
	"context"
	"errors"
	"fmt"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"

	"github.com/sonnes/gopi/internal/retry"
)

// openStream sends the request and returns the stream. It retries with
// the policy. The SDK sends the request before it returns the stream,
// so a retry happens before the first event.
func (p *Provider) openStream(
	ctx context.Context,
	params sdk.MessageNewParams,
	policy retry.Policy,
	reqOpts []option.RequestOption,
) (*ssestream.Stream[sdk.MessageStreamEventUnion], error) {
	send := func() (*ssestream.Stream[sdk.MessageStreamEventUnion], error) {
		stream := p.client.Messages.NewStreaming(ctx, params, reqOpts...)
		return stream, stream.Err()
	}

	stream, err := retry.Do(ctx, policy, send, retryStatus)
	if err != nil {
		return nil, fmt.Errorf("anthropic: %w", err)
	}
	return stream, nil
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
