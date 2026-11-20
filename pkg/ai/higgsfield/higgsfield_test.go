package higgsfield_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/higgsfield"
)

const (
	testKey    = "key-id:key-secret"
	soulPath   = "/higgsfield-ai/soul/v2/standard"
	statusPath = "/requests/r1/status"
	cancelPath = "/requests/r1/cancel"
	imagePath  = "/files/image-1.jpg"
)

var soulModel = ai.Model{ID: "higgsfield-ai/soul/v2/standard"}

// fakeAPI is a Higgsfield API on an httptest server. A submit answers
// with a queued request. Each status read returns the next status of
// statuses, and the last status repeats.
type fakeAPI struct {
	t        *testing.T
	srv      *httptest.Server
	statuses []string
	// final is the body of a status read that returns a final status.
	final map[string]any
	// imageType is the Content-Type of a download.
	imageType string

	mu         sync.Mutex
	submits    int
	submitBody map[string]any
	submitHead http.Header
	reads      int
	statusAuth string
	cancels    int
	imageAuth  string
	// readDone gets a value after the provider reads the body of each
	// status answer.
	readDone chan struct{}
}

func newFakeAPI(t *testing.T, statuses ...string) *fakeAPI {
	t.Helper()
	f := &fakeAPI{
		t:         t,
		statuses:  statuses,
		imageType: "image/jpeg",
		readDone:  make(chan struct{}, 100),
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == http.MethodPost && r.URL.Path == soulPath:
		f.submits++
		f.submitHead = r.Header.Clone()
		f.submitBody = nil
		require.NoError(f.t, json.NewDecoder(r.Body).Decode(&f.submitBody))
		writeJSON(w, map[string]any{
			"status":     "queued",
			"request_id": "r1",
			"status_url": f.srv.URL + statusPath,
			"cancel_url": f.srv.URL + cancelPath,
		})
	case r.Method == http.MethodGet && r.URL.Path == statusPath:
		f.statusAuth = r.Header.Get("Authorization")
		status := f.statuses[min(f.reads, len(f.statuses)-1)]
		f.reads++
		body := map[string]any{"status": status, "request_id": "r1"}
		if status != "queued" && status != "in_progress" {
			for k, v := range f.final {
				body[k] = v
			}
		}
		writeJSON(w, body)
	case r.Method == http.MethodPost && r.URL.Path == cancelPath:
		f.cancels++
		w.WriteHeader(http.StatusAccepted)
	case r.Method == http.MethodGet && r.URL.Path == imagePath:
		f.imageAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", f.imageType)
		_, _ = w.Write([]byte("jpeg bytes"))
	default:
		http.NotFound(w, r)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// provider returns a provider that calls f and polls with no wait.
func (f *fakeAPI) provider(opts ...higgsfield.Option) *higgsfield.Provider {
	base := []higgsfield.Option{
		higgsfield.APIKey(testKey),
		higgsfield.BaseURL(f.srv.URL + "/"),
		higgsfield.HTTPClient(&http.Client{Transport: signalTransport{
			base: f.srv.Client().Transport,
			done: f.readDone,
		}}),
		higgsfield.PollInterval(time.Millisecond),
	}
	return higgsfield.New(append(base, opts...)...)
}

// signalTransport sends on done when the provider closes the body of a
// status answer, so a test knows that a status read is over.
type signalTransport struct {
	base http.RoundTripper
	done chan<- struct{}
}

func (s signalTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := s.base.RoundTrip(r)
	if err == nil && r.URL.Path == statusPath {
		resp.Body = signalBody{ReadCloser: resp.Body, done: s.done}
	}
	return resp, err
}

type signalBody struct {
	io.ReadCloser
	done chan<- struct{}
}

func (b signalBody) Close() error {
	err := b.ReadCloser.Close()
	b.done <- struct{}{}
	return err
}

// completed sets the final body to one image at imagePath.
func (f *fakeAPI) completed() {
	f.final = map[string]any{
		"images": []map[string]any{{"url": f.srv.URL + imagePath}},
	}
}

func TestGenerateImage(t *testing.T) {
	f := newFakeAPI(t, "queued", "in_progress", "completed")
	f.completed()

	resp, err := f.provider().GenerateImage(t.Context(), soulModel, "a lake", nil)

	require.NoError(t, err)
	assert.Equal(t, &ai.ImageResponse{Images: []ai.GeneratedImage{{
		Data:      []byte("jpeg bytes"),
		MediaType: "image/jpeg",
	}}}, resp)
	assert.Equal(t, map[string]any{"prompt": "a lake"}, f.submitBody)
	assert.Equal(t, "Key "+testKey, f.submitHead.Get("Authorization"))
	assert.Equal(t, "application/json", f.submitHead.Get("Content-Type"))
	assert.NotEmpty(t, f.submitHead.Get("Idempotency-Key"))
	assert.Equal(t, 3, f.reads)
	assert.Equal(t, "Key "+testKey, f.statusAuth)
	assert.Empty(t, f.imageAuth, "a download must not send the key to the file host")
}

func TestGenerateImageOptions(t *testing.T) {
	tests := []struct {
		name string
		opts ai.Options
		want map[string]any
	}{
		{
			name: "supported aspect ratio",
			opts: ai.AspectRatio("2:3"),
			want: map[string]any{"aspect_ratio": "2:3"},
		},
		{
			name: "4:5 maps to 3:4",
			opts: ai.AspectRatio("4:5"),
			want: map[string]any{"aspect_ratio": "3:4"},
		},
		{
			name: "5:4 maps to 4:3",
			opts: ai.AspectRatio("5:4"),
			want: map[string]any{"aspect_ratio": "4:3"},
		},
		{
			name: "21:9 maps to 16:9",
			opts: ai.AspectRatio("21:9"),
			want: map[string]any{"aspect_ratio": "16:9"},
		},
		{
			name: "9:21 maps to 9:16",
			opts: ai.AspectRatio("9:21"),
			want: map[string]any{"aspect_ratio": "9:16"},
		},
		{
			name: "1K is 720p",
			opts: ai.ImageResolution("1K"),
			want: map[string]any{"resolution": "720p"},
		},
		{
			name: "2K is 1080p",
			opts: ai.ImageResolution("2K"),
			want: map[string]any{"resolution": "1080p"},
		},
		{
			name: "4K is 1080p",
			opts: ai.ImageResolution("4K"),
			want: map[string]any{"resolution": "1080p"},
		},
		{
			name: "one image",
			opts: ai.ImageCount(1),
			want: map[string]any{"batch_size": float64(1)},
		},
		{
			name: "two images is a batch of four",
			opts: ai.ImageCount(2),
			want: map[string]any{"batch_size": float64(4)},
		},
		{
			name: "character",
			opts: ai.Character("c0ffee"),
			want: map[string]any{
				"custom_reference_id":       "c0ffee",
				"custom_reference_strength": float64(1),
			},
		},
		{
			name: "size is ignored",
			opts: ai.ImageSize("1024x1024"),
			want: map[string]any{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeAPI(t, "completed")
			f.completed()

			_, err := f.provider().GenerateImage(t.Context(), soulModel, "a lake", tt.opts)

			require.NoError(t, err)
			want := map[string]any{"prompt": "a lake"}
			for k, v := range tt.want {
				want[k] = v
			}
			assert.Equal(t, want, f.submitBody)
		})
	}
}

func TestGenerateImageBadOption(t *testing.T) {
	tests := []struct {
		name string
		opts ai.Options
	}{
		{name: "aspect ratio", opts: ai.AspectRatio("wide")},
		{name: "resolution", opts: ai.ImageResolution("8K")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeAPI(t, "completed")

			_, err := f.provider().GenerateImage(t.Context(), soulModel, "a lake", tt.opts)

			require.ErrorContains(t, err, "higgsfield")
			assert.Equal(t, 0, f.submits)
		})
	}
}

func TestGenerateImageMediaType(t *testing.T) {
	tests := []struct {
		name      string
		imageType string
		url       string
		want      string
	}{
		{name: "from the header", imageType: "image/webp", url: imagePath, want: "image/webp"},
		{name: "from the extension", imageType: "application/octet-stream", url: imagePath, want: "image/jpeg"},
		{name: "default", imageType: "application/octet-stream", url: "/files/image", want: "image/png"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeAPI(t, "completed")
			f.imageType = tt.imageType
			mux := http.NewServeMux()
			mux.HandleFunc("/files/image", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tt.imageType)
				_, _ = w.Write([]byte("bytes"))
			})
			mux.HandleFunc("/", f.serve)
			f.srv.Config.Handler = mux
			f.final = map[string]any{
				"images": []map[string]any{{"url": f.srv.URL + tt.url}},
			}

			resp, err := f.provider().GenerateImage(t.Context(), soulModel, "a lake", nil)

			require.NoError(t, err)
			require.Len(t, resp.Images, 1)
			assert.Equal(t, tt.want, resp.Images[0].MediaType)
		})
	}
}

func TestGenerateImageFinalStatus(t *testing.T) {
	tests := []struct {
		status string
		final  map[string]any
		want   string
	}{
		{status: "failed", final: map[string]any{"error": "bad prompt"}, want: "bad prompt"},
		{status: "nsfw", want: "nsfw"},
		{status: "canceled", want: "canceled"},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			f := newFakeAPI(t, "queued", tt.status)
			f.final = tt.final

			_, err := f.provider().GenerateImage(t.Context(), soulModel, "a lake", nil)

			require.ErrorContains(t, err, tt.status)
			assert.ErrorContains(t, err, tt.want)
			assert.ErrorContains(t, err, "higgsfield")
		})
	}
}

func TestGenerateImageCancel(t *testing.T) {
	f := newFakeAPI(t, "queued")
	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		<-f.readDone
		cancel()
	}()

	_, err := f.provider(higgsfield.PollInterval(time.Hour)).GenerateImage(ctx, soulModel, "a lake", nil)

	require.ErrorIs(t, err, context.Canceled)
	f.mu.Lock()
	defer f.mu.Unlock()
	assert.Equal(t, 1, f.cancels)
}

func TestGenerateImageNoCancelInProgress(t *testing.T) {
	f := newFakeAPI(t, "in_progress")
	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		<-f.readDone
		cancel()
	}()

	_, err := f.provider(higgsfield.PollInterval(time.Hour)).GenerateImage(ctx, soulModel, "a lake", nil)

	require.ErrorIs(t, err, context.Canceled)
	f.mu.Lock()
	defer f.mu.Unlock()
	assert.Equal(t, 0, f.cancels, "the API cancels only a queued request")
}

func TestGenerateImageTimeout(t *testing.T) {
	f := newFakeAPI(t, "in_progress")

	_, err := f.provider(higgsfield.PollTimeout(20*time.Millisecond)).GenerateImage(
		t.Context(),
		soulModel,
		"a lake",
		nil,
	)

	require.ErrorContains(t, err, "higgsfield")
	assert.ErrorContains(t, err, "in_progress")
}

func TestErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail":"Invalid credentials"}`))
	}))
	t.Cleanup(srv.Close)
	p := higgsfield.New(
		higgsfield.APIKey(testKey),
		higgsfield.BaseURL(srv.URL),
		higgsfield.HTTPClient(srv.Client()),
	)

	_, err := p.GenerateImage(t.Context(), soulModel, "a lake", nil)

	var apiErr *higgsfield.Error
	require.True(t, errors.As(err, &apiErr), "got %v", err)
	assert.Equal(t, http.StatusUnauthorized, apiErr.StatusCode)
	assert.Contains(t, apiErr.Body, "Invalid credentials")
	assert.ErrorContains(t, err, "higgsfield")
}

func TestRetrySubmit(t *testing.T) {
	f := newFakeAPI(t, "completed")
	f.completed()
	var keys []string
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+soulPath, func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		if len(keys) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		f.serve(w, r)
	})
	mux.HandleFunc("/", f.serve)
	f.srv.Config.Handler = mux

	_, err := f.provider().GenerateImage(t.Context(), soulModel, "a lake", ai.MaxRetries(1))

	require.NoError(t, err)
	require.Len(t, keys, 2)
	assert.Equal(t, keys[0], keys[1], "a retry sends the same idempotency key")
}

func TestListModels(t *testing.T) {
	got, err := higgsfield.New().ListModels(t.Context())

	require.NoError(t, err)
	assert.Equal(t, []ai.Model{
		{
			ID:     "higgsfield-ai/soul/v2/standard",
			Name:   "SOUL V2",
			Input:  []ai.Modality{ai.ModalityText},
			Output: []ai.Output{ai.OutputImage},
		},
		{
			ID:     "higgsfield-ai/soul/cinema",
			Name:   "SOUL Cinema",
			Input:  []ai.Modality{ai.ModalityText},
			Output: []ai.Output{ai.OutputImage},
		},
	}, got)
}
