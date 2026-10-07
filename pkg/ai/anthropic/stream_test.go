package anthropic

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
)

// sseBody is a short stream that ends with stopReason.
func sseBody(stopReason string) string {
	return "event: message_start\n" +
		`data: {"type":"message_start","message":{"id":"m1","type":"message","role":"assistant","model":"claude-test","content":[],"usage":{"input_tokens":5,"output_tokens":1}}}` + "\n\n" +
		"event: content_block_start\n" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}` + "\n\n" +
		"event: content_block_stop\n" +
		`data: {"type":"content_block_stop","index":0}` + "\n\n" +
		"event: message_delta\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":` + stopReason + `},"usage":{"output_tokens":2}}` + "\n\n" +
		"event: message_stop\n" +
		`data: {"type":"message_stop"}` + "\n\n"
}

// fakeAPI answers each request with the next handler, and repeats the
// last one. It counts the requests.
type fakeAPI struct {
	handlers []http.HandlerFunc
	calls    atomic.Int32
	last     *http.Request
	body     []byte
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	n := int(f.calls.Add(1)) - 1
	f.last = r
	f.body, _ = io.ReadAll(r.Body)
	f.handlers[min(n, len(f.handlers)-1)](w, r)
}

// ok answers with a stream that ends with end_turn.
func ok(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = io.WriteString(w, sseBody(`"end_turn"`))
}

// status answers with an API error and the headers.
func status(code int, headers map[string]string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"api_error","message":"busy"}}`)
	}
}

func newFakeProvider(t *testing.T, api *fakeAPI) *Provider {
	t.Helper()
	server := httptest.NewServer(api)
	t.Cleanup(server.Close)
	return New(
		APIKey("test"),
		BaseURL(server.URL),
	)
}

func TestStreamText_Retry(t *testing.T) {
	api := &fakeAPI{handlers: []http.HandlerFunc{
		status(http.StatusTooManyRequests, map[string]string{"retry-after-ms": "1"}),
		status(http.StatusServiceUnavailable, map[string]string{"retry-after-ms": "1"}),
		ok,
	}}
	p := newFakeProvider(t, api)

	msg, err := p.StreamText(
		t.Context(),
		testModel,
		[]ai.Message{ai.UserMessage("Hi")},
		ai.MaxRetries(2),
	).Wait()
	require.NoError(t, err)
	assert.Equal(t, "Hi", msg.Text())
	assert.Equal(t, int32(3), api.calls.Load())
}

func TestStreamText_NoRetryByDefault(t *testing.T) {
	api := &fakeAPI{handlers: []http.HandlerFunc{status(http.StatusTooManyRequests, nil), ok}}
	p := newFakeProvider(t, api)

	_, err := p.StreamText(t.Context(), testModel, []ai.Message{ai.UserMessage("Hi")}, nil).Wait()
	require.Error(t, err)
	assert.Equal(t, int32(1), api.calls.Load())
}

func TestStreamText_NoRetryOnClientError(t *testing.T) {
	api := &fakeAPI{handlers: []http.HandlerFunc{status(http.StatusBadRequest, nil), ok}}
	p := newFakeProvider(t, api)

	_, err := p.StreamText(
		t.Context(),
		testModel,
		[]ai.Message{ai.UserMessage("Hi")},
		ai.MaxRetries(3),
	).Wait()
	require.Error(t, err)

	var apiErr *Error
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
	assert.Equal(t, int32(1), api.calls.Load())
}

func TestStreamText_RetryDelayCap(t *testing.T) {
	api := &fakeAPI{handlers: []http.HandlerFunc{
		status(http.StatusTooManyRequests, map[string]string{"retry-after": "120"}),
		ok,
	}}
	p := newFakeProvider(t, api)

	_, err := p.StreamText(
		t.Context(),
		testModel,
		[]ai.Message{ai.UserMessage("Hi")},
		ai.MaxRetries(3),
	).Wait()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "retry")
	assert.Equal(t, int32(1), api.calls.Load(), "a wait over the cap fails at once")
}

func TestRetryDelay(t *testing.T) {
	header := func(k, v string) http.Header {
		h := http.Header{}
		h.Set(k, v)
		return h
	}

	assert.Equal(t, 250*time.Millisecond, serverDelay(header("retry-after-ms", "250")))
	assert.Equal(t, 3*time.Second, serverDelay(header("retry-after", "3")))
	assert.Zero(t, serverDelay(http.Header{}))

	for i := range 6 {
		d := backoff(i)
		assert.LessOrEqual(t, d, maxBackoff)
		assert.Positive(t, d)
	}
}

func TestStreamText_Headers(t *testing.T) {
	api := &fakeAPI{handlers: []http.HandlerFunc{ok}}
	p := newFakeProvider(t, api)

	_, err := p.StreamText(
		t.Context(),
		testModel,
		[]ai.Message{ai.UserMessage("Hi")},
		ai.Headers(map[string]string{"X-Team": "infra"}),
	).Wait()
	require.NoError(t, err)
	assert.Equal(t, "infra", api.last.Header.Get("X-Team"))
}

func TestStreamText_Betas(t *testing.T) {
	api := &fakeAPI{handlers: []http.HandlerFunc{ok}}
	p := newFakeProvider(t, api)

	_, err := p.StreamText(t.Context(), Opus5_5, []ai.Message{ai.UserMessage("Hi")}, nil).Wait()
	require.NoError(t, err)
	assert.Equal(
		t,
		[]string{midConversationOutputConfigBeta, thinkingBindingControlsBeta},
		api.last.Header.Values("anthropic-beta"),
	)
}

func TestStreamText_OnPayload(t *testing.T) {
	api := &fakeAPI{handlers: []http.HandlerFunc{ok}}
	p := newFakeProvider(t, api)

	var seen string
	hook := func(params *sdk.MessageNewParams) error {
		seen = string(params.Model)
		params.MaxTokens = 7
		return nil
	}
	_, err := p.StreamText(
		t.Context(),
		testModel,
		[]ai.Message{ai.UserMessage("Hi")},
		OnPayload(hook),
	).Wait()
	require.NoError(t, err)
	assert.Equal(t, testModel.ID, seen)

	var body map[string]any
	require.NoError(t, json.Unmarshal(api.body, &body))
	assert.Equal(t, 7.0, body["max_tokens"], "the hook changes the request")
}

func TestStreamText_NoStopReason(t *testing.T) {
	api := &fakeAPI{handlers: []http.HandlerFunc{func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sseBody("null"))
	}}}
	p := newFakeProvider(t, api)

	_, err := p.StreamText(t.Context(), testModel, []ai.Message{ai.UserMessage("Hi")}, nil).Wait()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no stop reason")
}

func TestStreamText_Effort(t *testing.T) {
	api := &fakeAPI{handlers: []http.HandlerFunc{ok}}
	p := newFakeProvider(t, api)

	msg, err := p.StreamText(
		t.Context(),
		Opus5_5,
		[]ai.Message{ai.UserMessage("Hi")},
		ai.Thinking(ai.ThinkingLow),
	).Wait()
	require.NoError(t, err)
	assert.Equal(t, "low", msg.Effort, "the message records the effort")
}
