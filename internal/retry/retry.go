// Package retry sends a provider request again after an error that can
// pass, such as a rate limit or an overload. Every provider uses it, so
// they share one policy.
//
// A caller must obey these rules:
//
//   - The SDK of the provider must not retry, so the wait has a cap.
//   - Send must make one request and return before the first event of
//     the stream. A retry after an event repeats output.
//
// The reasons are in docs/concepts/ai/provider.md.
package retry

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"

	"github.com/sonnes/gopi/pkg/ai"
)

const (
	// DefaultMaxDelay is the longest wait that a server can ask for
	// when [ai.MaxRetryDelay] is unset.
	DefaultMaxDelay = 60 * time.Second
	// firstBackoff is the wait before the first retry when the server
	// asks for no wait. Each retry doubles it.
	firstBackoff = 500 * time.Millisecond
	// maxBackoff caps the doubled wait.
	maxBackoff = 8 * time.Second
	// backoffJitter is the largest part of a wait that is removed at
	// random, so many clients do not retry at the same time.
	backoffJitter = 0.25
)

// Policy is how a call retries a request that fails.
type Policy struct {
	// MaxRetries is how many times [Do] sends the request again. If
	// [ai.MaxRetries] is unset, [PolicyOf] sets it to zero, so the call
	// does not retry.
	MaxRetries int
	// MaxDelay caps the wait that a server can ask for. Zero removes
	// the cap. If [ai.MaxRetryDelay] is unset, [PolicyOf] sets it to
	// [DefaultMaxDelay].
	MaxDelay time.Duration
}

// PolicyOf reads [ai.MaxRetries] and [ai.MaxRetryDelay]. With no
// options, a call does not retry.
func PolicyOf(opts ai.Options) Policy {
	policy := Policy{MaxDelay: DefaultMaxDelay}
	if n, ok := ai.GetOption(opts, ai.MaxRetries); ok {
		policy.MaxRetries = n
	}
	if d, ok := ai.GetOption(opts, ai.MaxRetryDelay); ok {
		policy.MaxDelay = d
	}
	return policy
}

// Status is the HTTP status and the headers of a failed request.
type Status struct {
	Code   int
	Header http.Header
}

// Do calls send until it returns no error, an error that cannot pass,
// or the policy has no retries left. status reads the [Status] from an
// error of the SDK. It returns false for an error with no response,
// such as a dropped connection, which can pass.
//
// Do returns the last error of send as it is. If the server asks for a
// wait over the cap, Do returns an error that wraps it.
func Do[T any](
	ctx context.Context,
	policy Policy,
	send func() (T, error),
	status func(error) (Status, bool),
) (T, error) {
	for attempt := 0; ; attempt++ {
		out, err := send()
		if err == nil {
			return out, nil
		}

		st, hasStatus := status(err)
		canRetry := attempt < policy.MaxRetries && ctx.Err() == nil && retryable(st, hasStatus)
		if !canRetry {
			return out, err
		}

		wait, err := policy.wait(st, attempt, err)
		if err != nil {
			return out, err
		}

		select {
		case <-ctx.Done():
			return out, ctx.Err()
		case <-time.After(wait):
		}
	}
}

// wait returns the time to wait before the retry after err. The server
// can ask for a wait. If it asks for more than the cap, wait returns an
// error, because a long wait looks like a hung call.
func (policy Policy) wait(st Status, attempt int, err error) (time.Duration, error) {
	asked := serverDelay(st.Header)
	overCap := policy.MaxDelay > 0 && asked > policy.MaxDelay
	if overCap {
		return 0, fmt.Errorf(
			"the server asked for a retry after %s, more than the cap of %s: %w",
			asked,
			policy.MaxDelay,
			err,
		)
	}
	if asked > 0 {
		return asked, nil
	}
	return backoff(attempt), nil
}

// retryable reports if a retry can pass. The server can say so in a
// header. Else a timeout, a conflict, a rate limit, and a server error
// can pass. An error with no status can also pass.
func retryable(st Status, hasStatus bool) bool {
	if !hasStatus {
		return true
	}

	switch st.Header.Get("x-should-retry") {
	case "true":
		return true
	case "false":
		return false
	}

	switch code := st.Code; {
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
