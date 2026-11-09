package providertest

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
)

// Fake is the wire format of one provider, for the cases that run on a
// fake API. The cases check that each call of the provider goes through
// the shared request path: it retries, and it marks a refused size.
type Fake struct {
	// New returns the provider for the fake API at baseURL.
	New func(baseURL string) ai.TextProvider
	// Text answers a call of StreamText with a stream of the text "Hi".
	Text http.HandlerFunc
	// Object answers a call of GenerateObject with an object and its
	// usage. Nil skips the object cases.
	Object http.HandlerFunc
	// Count answers a call of CountTokens with 7 input tokens. Nil skips
	// the token cases.
	Count http.HandlerFunc
}

// RunFake runs every case on a fake API as a subtest of t.
func RunFake(t *testing.T, f Fake) {
	t.Run("StreamText", f.streamText)
	t.Run("GenerateObject", f.generateObject)
	t.Run("CountTokens", f.countTokens)
}

// fakeAPI answers each request with the next handler, and repeats the
// last one. It counts the requests.
type fakeAPI struct {
	handlers []http.HandlerFunc
	calls    atomic.Int32
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	n := int(f.calls.Add(1)) - 1
	f.handlers[min(n, len(f.handlers)-1)](w, r)
}

// failure answers with an API error with status code and the headers
// in kv, as pairs of a key and a value.
func failure(code int, kv ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		for i := 0; i+1 < len(kv); i += 2 {
			w.Header().Set(kv[i], kv[i+1])
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		fmt.Fprint(w, `{"type":"error","error":{"type":"api_error","message":"busy"}}`)
	}
}

// shortWait is the header in which a server asks for a wait in
// milliseconds.
const shortWait = "retry-after-ms"

// serve starts a fake API with handlers, and returns the provider for
// it.
func (f Fake) serve(t *testing.T, handlers ...http.HandlerFunc) (ai.TextProvider, *fakeAPI) {
	t.Helper()
	api := &fakeAPI{handlers: handlers}
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	return f.New(srv.URL), api
}

func (f Fake) streamText(t *testing.T) {
	tests := []struct {
		name      string
		handlers  []http.HandlerFunc
		opts      ai.Options
		wantCalls int32
		wantErr   bool
		wantLarge bool
	}{
		{
			name: "retries a rate limit and an overload",
			handlers: []http.HandlerFunc{
				failure(http.StatusTooManyRequests, shortWait, "1"),
				failure(http.StatusServiceUnavailable, shortWait, "1"),
				f.Text,
			},
			opts:      ai.MaxRetries(2),
			wantCalls: 3,
		},
		{
			name:      "no retry by default",
			handlers:  []http.HandlerFunc{failure(http.StatusTooManyRequests), f.Text},
			wantCalls: 1,
			wantErr:   true,
		},
		{
			name:      "no retry of a client error",
			handlers:  []http.HandlerFunc{failure(http.StatusBadRequest), f.Text},
			opts:      ai.MaxRetries(3),
			wantCalls: 1,
			wantErr:   true,
		},
		{
			name:      "a wait over the cap fails at once",
			handlers:  []http.HandlerFunc{failure(http.StatusTooManyRequests, "retry-after", "120"), f.Text},
			opts:      ai.MaxRetries(3),
			wantCalls: 1,
			wantErr:   true,
		},
		{
			name:      "marks a refused size",
			handlers:  []http.HandlerFunc{failure(http.StatusRequestEntityTooLarge)},
			wantCalls: 1,
			wantErr:   true,
			wantLarge: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, api := f.serve(t, tt.handlers...)

			msg, err := p.StreamText(t.Context(), fakeModel, []ai.Message{ai.UserMessage("Hi")}, tt.opts).Wait()

			assert.Equal(t, tt.wantCalls, api.calls.Load())
			if !tt.wantErr {
				require.NoError(t, err)
				assert.Equal(t, "Hi", msg.Text())
				return
			}
			require.Error(t, err)
			assert.Equal(t, tt.wantLarge, errors.Is(err, ai.ErrContextExceeded))
		})
	}
}

func (f Fake) generateObject(t *testing.T) {
	if f.Object == nil {
		t.Skip("the provider does not generate objects")
	}
	priced := fakeModel
	priced.Pricing = ai.Pricing{
		Input:  1,
		Output: 2,
	}
	call := func(t *testing.T, opts ai.Options, handlers ...http.HandlerFunc) (*ai.ObjectResponse, *fakeAPI, error) {
		p, api := f.serve(t, handlers...)
		objects, ok := p.(ai.ObjectProvider)
		require.True(t, ok, "the provider generates objects")
		resp, err := objects.GenerateObject(
			t.Context(),
			priced,
			[]ai.Message{ai.UserMessage("Hi")},
			&jsonschema.Schema{Type: "object"},
			opts,
		)
		return resp, api, err
	}

	t.Run("reports the cost", func(t *testing.T) {
		resp, _, err := call(t, nil, f.Object)
		require.NoError(t, err)
		assert.Equal(t, priced.Pricing.Cost(resp.Usage), resp.Usage.Cost)
		assert.Positive(t, resp.Usage.Cost.Total())
	})

	t.Run("retries", func(t *testing.T) {
		_, api, err := call(t, ai.MaxRetries(1), failure(http.StatusServiceUnavailable, shortWait, "1"), f.Object)
		require.NoError(t, err)
		assert.Equal(t, int32(2), api.calls.Load())
	})

	t.Run("marks a refused size", func(t *testing.T) {
		_, _, err := call(t, nil, failure(http.StatusRequestEntityTooLarge))
		assert.ErrorIs(t, err, ai.ErrContextExceeded)
	})
}

func (f Fake) countTokens(t *testing.T) {
	if f.Count == nil {
		t.Skip("the provider does not count tokens")
	}
	count := func(t *testing.T, opts ai.Options, handlers ...http.HandlerFunc) (*ai.TokenCount, *fakeAPI, error) {
		p, api := f.serve(t, handlers...)
		counter, ok := p.(ai.TokenCounter)
		require.True(t, ok, "the provider counts tokens")
		got, err := counter.CountTokens(t.Context(), fakeModel, []ai.Message{ai.UserMessage("Hi")}, opts)
		return got, api, err
	}

	t.Run("retries", func(t *testing.T) {
		got, api, err := count(t, ai.MaxRetries(1), failure(http.StatusServiceUnavailable, shortWait, "1"), f.Count)
		require.NoError(t, err)
		assert.Equal(t, 7, got.Total)
		assert.Equal(t, int32(2), api.calls.Load())
	})

	t.Run("marks a refused size", func(t *testing.T) {
		_, _, err := count(t, nil, failure(http.StatusRequestEntityTooLarge))
		assert.ErrorIs(t, err, ai.ErrContextExceeded)
	})
}

// fakeModel is the model of the cases on a fake API.
var fakeModel = ai.Model{
	ID:        "fake-model",
	MaxTokens: 1024,
}
