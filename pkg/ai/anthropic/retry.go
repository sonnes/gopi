package anthropic

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"

	"github.com/sonnes/gopi/pkg/ai"
)

const (
	// defaultMaxRetryDelay is the longest wait that a server can ask
	// for when [ai.MaxRetryDelay] is unset.
	defaultMaxRetryDelay = 60 * time.Second
	// firstBackoff is the wait before the first retry when the server
	// asks for no wait. Each retry doubles it.
	firstBackoff = 500 * time.Millisecond
	// maxBackoff caps the doubled wait.
	maxBackoff = 8 * time.Second
	// backoffJitter is the largest part of a wait that is removed at
	// random, so many clients do not retry at the same time.
	backoffJitter = 0.25
)

// retryPolicy is how a call retries a request that fails.
type retryPolicy struct {
	maxRetries int
	// maxDelay caps the wait that a server can ask for. Zero removes
	// the cap.
	maxDelay time.Duration
}

// retryPolicyOf reads the retry options. With no options, a call does
// not retry.
func retryPolicyOf(opts ai.Options) retryPolicy {
	policy := retryPolicy{maxDelay: defaultMaxRetryDelay}
	if n, ok := ai.GetOption(opts, ai.MaxRetries); ok {
		policy.maxRetries = n
	}
	if d, ok := ai.GetOption(opts, ai.MaxRetryDelay); ok {
		policy.maxDelay = d
	}
	return policy
}

// openStream sends the request and returns the stream. If the request
// fails with an error that can pass, it sends the request again, up to
// the limit of the policy. The SDK sends the request before it returns
// the stream, so a retry happens before the first event.
//
// The provider retries itself, and not in the SDK, so that a wait that
// the server asks for has a cap.
func (p *Provider) openStream(
	ctx context.Context,
	params sdk.MessageNewParams,
	policy retryPolicy,
	reqOpts []option.RequestOption,
) (*ssestream.Stream[sdk.MessageStreamEventUnion], error) {
	for attempt := 0; ; attempt++ {
		stream := p.client.Messages.NewStreaming(ctx, params, reqOpts...)
		err := stream.Err()
		if err == nil {
			return stream, nil
		}

		canRetry := attempt < policy.maxRetries && ctx.Err() == nil && retryable(err)
		if !canRetry {
			return nil, fmt.Errorf("anthropic: %w", err)
		}

		wait, err := policy.wait(err, attempt)
		if err != nil {
			return nil, err
		}

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("anthropic: %w", ctx.Err())
		case <-time.After(wait):
		}
	}
}

// wait returns the time to wait before the retry after err. The server
// can ask for a wait. If it asks for more than the cap, wait returns an
// error, because a long wait looks like a hung call.
func (policy retryPolicy) wait(err error, attempt int) (time.Duration, error) {
	var apiErr *sdk.Error
	if errors.As(err, &apiErr) && apiErr.Response != nil {
		asked := serverDelay(apiErr.Response.Header)
		overCap := policy.maxDelay > 0 && asked > policy.maxDelay
		if overCap {
			return 0, fmt.Errorf(
				"anthropic: the server asked for a retry after %s, more than the cap of %s: %w",
				asked,
				policy.maxDelay,
				err,
			)
		}
		if asked > 0 {
			return asked, nil
		}
	}
	return backoff(attempt), nil
}

// retryable reports if a retry of the request can pass. The server can
// say so in a header. Else a timeout, a conflict, a rate limit, and a
// server error can pass. An error with no response, such as a dropped
// connection, can also pass.
func retryable(err error) bool {
	var apiErr *sdk.Error
	if !errors.As(err, &apiErr) {
		return true
	}

	if apiErr.Response != nil {
		switch apiErr.Response.Header.Get("x-should-retry") {
		case "true":
			return true
		case "false":
			return false
		}
	}

	switch code := apiErr.StatusCode; {
	case code == http.StatusRequestTimeout,
		code == http.StatusConflict,
		code == http.StatusTooManyRequests,
		code >= http.StatusInternalServerError:
		return true
	default:
		return false
	}
}

// serverDelay reads the wait that the server asks for from
// retry-after-ms, then retry-after. Retry-after holds seconds or an
// HTTP date. It returns zero if the server asks for no wait.
func serverDelay(h http.Header) time.Duration {
	if v := h.Get("retry-after-ms"); v != "" {
		if ms, err := strconv.ParseFloat(v, 64); err == nil {
			return time.Duration(ms * float64(time.Millisecond))
		}
	}

	v := h.Get("retry-after")
	if v == "" {
		return 0
	}
	if seconds, err := strconv.ParseFloat(v, 64); err == nil {
		return time.Duration(seconds * float64(time.Second))
	}
	if at, err := http.ParseTime(v); err == nil {
		return max(0, time.Until(at))
	}
	return 0
}

// backoff returns the wait before retry attempt when the server asks
// for none. The wait doubles each time, up to maxBackoff, less a random
// part.
func backoff(attempt int) time.Duration {
	doubled := float64(firstBackoff) * math.Pow(2, float64(attempt))
	wait := min(doubled, float64(maxBackoff))
	return time.Duration(wait * (1 - rand.Float64()*backoffJitter))
}
