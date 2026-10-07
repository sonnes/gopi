package retry

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
)

// apiError is an error of a fake SDK with a status and headers.
type apiError struct {
	code   int
	header http.Header
}

func (e *apiError) Error() string { return http.StatusText(e.code) }

// status reads an apiError, as a provider reads the error of its SDK.
func status(err error) (Status, bool) {
	var e *apiError
	if !errors.As(err, &e) {
		return Status{}, false
	}
	return Status{Code: e.code, Header: e.header}, true
}

// fakeSend returns the errors in order, then "ok".
func fakeSend(calls *int, errs ...error) func() (string, error) {
	return func() (string, error) {
		*calls++
		if *calls <= len(errs) {
			return "", errs[*calls-1]
		}
		return "ok", nil
	}
}

func header(k, v string) http.Header {
	h := http.Header{}
	h.Set(k, v)
	return h
}

func TestDo(t *testing.T) {
	short := header("retry-after-ms", "1")
	tests := []struct {
		name      string
		policy    Policy
		errs      []error
		wantCalls int
		wantErr   bool
	}{
		{
			name:      "retries a rate limit and an overload",
			policy:    Policy{MaxRetries: 2, MaxDelay: time.Minute},
			errs:      []error{&apiError{429, short}, &apiError{503, short}},
			wantCalls: 3,
		},
		{
			name:      "no retry by default",
			policy:    Policy{MaxDelay: time.Minute},
			errs:      []error{&apiError{429, short}},
			wantCalls: 1,
			wantErr:   true,
		},
		{
			name:      "no retry of a client error",
			policy:    Policy{MaxRetries: 3, MaxDelay: time.Minute},
			errs:      []error{&apiError{400, nil}},
			wantCalls: 1,
			wantErr:   true,
		},
		{
			name:      "the server says no",
			policy:    Policy{MaxRetries: 3, MaxDelay: time.Minute},
			errs:      []error{&apiError{503, header("x-should-retry", "false")}},
			wantCalls: 1,
			wantErr:   true,
		},
		{
			name:      "a wait over the cap fails at once",
			policy:    Policy{MaxRetries: 3, MaxDelay: time.Minute},
			errs:      []error{&apiError{429, header("retry-after", "120")}},
			wantCalls: 1,
			wantErr:   true,
		},
		{
			name:      "an error with no status can pass",
			policy:    Policy{MaxRetries: 1, MaxDelay: time.Minute},
			errs:      []error{errors.New("connection reset")},
			wantCalls: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls int
			got, err := Do(t.Context(), tt.policy, fakeSend(&calls, tt.errs...), status)
			assert.Equal(t, tt.wantCalls, calls)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "ok", got)
		})
	}
}

func TestDo_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	var calls int
	_, err := Do(ctx, Policy{MaxRetries: 3}, fakeSend(&calls, &apiError{503, nil}), status)
	require.Error(t, err)
	assert.Equal(t, 1, calls, "a canceled call does not retry")
}

func TestPolicyOf(t *testing.T) {
	assert.Equal(t, Policy{MaxDelay: DefaultMaxDelay}, PolicyOf(nil))
	assert.Equal(
		t,
		Policy{MaxRetries: 2, MaxDelay: 0},
		PolicyOf(ai.JoinOptions(ai.MaxRetries(2), ai.MaxRetryDelay(0))),
	)
}

func TestServerDelay(t *testing.T) {
	assert.Equal(t, 250*time.Millisecond, serverDelay(header("retry-after-ms", "250")))
	assert.Equal(t, 3*time.Second, serverDelay(header("retry-after", "3")))
	assert.Zero(t, serverDelay(http.Header{}))
	assert.Zero(t, serverDelay(nil))

	for i := range 6 {
		d := backoff(i)
		assert.LessOrEqual(t, d, maxBackoff)
		assert.Positive(t, d)
	}
}
