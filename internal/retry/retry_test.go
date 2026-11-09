package retry

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"testing/synctest"
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
		policy    retryPolicy
		errs      []error
		wantCalls int
		wantErr   bool
		// wantWait is the range of the time that the retries wait.
		wantWait [2]time.Duration
	}{
		{
			name:      "retries a rate limit and an overload",
			policy:    retryPolicy{MaxRetries: 2, MaxDelay: time.Minute},
			errs:      []error{&apiError{429, short}, &apiError{503, short}},
			wantCalls: 3,
			wantWait:  [2]time.Duration{2 * time.Millisecond, 2 * time.Millisecond},
		},
		{
			name:      "no retry by default",
			policy:    retryPolicy{MaxDelay: time.Minute},
			errs:      []error{&apiError{429, short}},
			wantCalls: 1,
			wantErr:   true,
		},
		{
			name:      "no retry of a client error",
			policy:    retryPolicy{MaxRetries: 3, MaxDelay: time.Minute},
			errs:      []error{&apiError{400, nil}},
			wantCalls: 1,
			wantErr:   true,
		},
		{
			name:      "the server says no",
			policy:    retryPolicy{MaxRetries: 3, MaxDelay: time.Minute},
			errs:      []error{&apiError{503, header("x-should-retry", "false")}},
			wantCalls: 1,
			wantErr:   true,
		},
		{
			name:      "a wait over the cap fails at once",
			policy:    retryPolicy{MaxRetries: 3, MaxDelay: time.Minute},
			errs:      []error{&apiError{429, header("retry-after", "120")}},
			wantCalls: 1,
			wantErr:   true,
		},
		{
			name:      "an error with no status can pass",
			policy:    retryPolicy{MaxRetries: 1, MaxDelay: time.Minute},
			errs:      []error{errors.New("connection reset")},
			wantCalls: 2,
			wantWait:  [2]time.Duration{firstBackoff * 3 / 4, firstBackoff},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var calls int
				start := time.Now()
				got, err := do(t.Context(), tt.policy, fakeSend(&calls, tt.errs...), status)
				waited := time.Since(start)
				assert.Equal(t, tt.wantCalls, calls)
				assert.GreaterOrEqual(t, waited, tt.wantWait[0])
				assert.LessOrEqual(t, waited, tt.wantWait[1])
				if tt.wantErr {
					require.Error(t, err)
					return
				}
				require.NoError(t, err)
				assert.Equal(t, "ok", got)
			})
		})
	}
}

func TestDo_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	var calls int
	_, err := do(ctx, retryPolicy{MaxRetries: 3}, fakeSend(&calls, &apiError{503, nil}), status)
	require.Error(t, err)
	assert.Equal(t, 1, calls, "a canceled call does not retry")
}

func TestPolicyOf(t *testing.T) {
	assert.Equal(t, retryPolicy{MaxDelay: defaultMaxDelay}, policyOf(nil))
	assert.Equal(
		t,
		retryPolicy{MaxRetries: 2, MaxDelay: 0},
		policyOf(ai.JoinOptions(ai.MaxRetries(2), ai.MaxRetryDelay(0))),
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

func TestMarkContextExceeded(t *testing.T) {
	status := func(code int) func(error) (Status, bool) {
		return func(error) (Status, bool) { return Status{Code: code}, true }
	}
	notAPI := func(error) (Status, bool) { return Status{}, false }

	tests := []struct {
		name   string
		err    string
		status func(error) (Status, bool)
		want   bool
	}{
		{"anthropic", "prompt is too long: 210000 tokens > 200000 maximum", status(400), true},
		{"anthropic max tokens", "input length and max_tokens exceed context limit", status(400), true},
		{"openai code", `"code":"context_length_exceeded"`, status(400), true},
		{"a compatible server", "maximum context length is 8192", status(400), true},
		{"a large body", "request entity too large", status(413), true},
		{"other client error", "invalid model", status(400), false},
		{"a server error with the words", "prompt is too long", status(500), false},
		{"no status", "prompt is too long", notAPI, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := markContextExceeded(errors.New(tt.err), tt.status)
			assert.Equal(t, tt.want, errors.Is(got, ai.ErrContextExceeded))
			assert.ErrorContains(t, got, tt.err)
		})
	}
}

func TestCall(t *testing.T) {
	short := header("retry-after-ms", "1")
	tests := []struct {
		name        string
		opts        ai.Options
		errs        []error
		wantCalls   int
		wantErr     string
		wantTooLong bool
	}{
		{
			name:      "retries with the policy of the options",
			opts:      ai.MaxRetries(1),
			errs:      []error{&apiError{503, short}},
			wantCalls: 2,
		},
		{
			name:      "adds the name of the provider",
			errs:      []error{&apiError{503, nil}},
			wantCalls: 1,
			wantErr:   "fake: Service Unavailable",
		},
		{
			name:        "marks a request that is too large",
			errs:        []error{&apiError{413, nil}},
			wantCalls:   1,
			wantErr:     "fake: ",
			wantTooLong: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var calls int
				got, err := Call(t.Context(), tt.opts, "fake", status, fakeSend(&calls, tt.errs...))
				assert.Equal(t, tt.wantCalls, calls)
				if tt.wantErr == "" {
					require.NoError(t, err)
					assert.Equal(t, "ok", got)
					return
				}
				require.ErrorContains(t, err, tt.wantErr)
				assert.Equal(t, tt.wantTooLong, errors.Is(err, ai.ErrContextExceeded))
			})
		})
	}
}
