package anthropic

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/internal/providertest"
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

func TestRequestPath(t *testing.T) {
	providertest.RunFake(t, providertest.Fake{
		New: func(baseURL string) ai.TextProvider {
			return New(
				APIKey("test"),
				BaseURL(baseURL),
			)
		},
		Text:   ok,
		Object: ok,
		Count:  counted,
	})
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

	_, err := p.StreamText(t.Context(), ai.Model{ID: "claude-opus-5-5"}, []ai.Message{ai.UserMessage("Hi")}, nil).Wait()
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
		ai.Model{ID: "claude-opus-5-5"},
		[]ai.Message{ai.UserMessage("Hi")},
		ai.Thinking(ai.ThinkingLow),
	).Wait()
	require.NoError(t, err)
	assert.Equal(t, "low", msg.Effort, "the message records the effort")
}

func TestStreamText_ContextExceeded(t *testing.T) {
	tooLong := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"invalid_request_error","message":"prompt is too long: 210000 tokens > 200000 maximum"}}`)
	}
	p := newFakeProvider(t, &fakeAPI{handlers: []http.HandlerFunc{tooLong}})

	_, err := p.StreamText(t.Context(), testModel, []ai.Message{ai.UserMessage("Hi")}, nil).Wait()

	assert.ErrorIs(t, err, ai.ErrContextExceeded)
}

// counted answers a token count.
func counted(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"input_tokens":7}`)
}
