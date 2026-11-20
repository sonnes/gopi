// Package elevenlabs is the provider for the ElevenLabs API. It speaks
// a text with the text-to-speech API, and transcribes an audio file
// with the speech-to-text API. It implements [ai.SpeechProvider],
// [ai.TranscriptionProvider], [ai.ModelLister], and [ai.KeyChecker]. It
// has no text generation.
//
// A caller must obey these rules:
//
//   - [ai.Voice] is a voice ID of ElevenLabs, not a voice name. If it
//     is unset, the provider uses [DefaultVoice].
//   - [Provider.ListModels] lists only the speech models, because the
//     models API of ElevenLabs does not list the transcription models.
//   - A failed answer of the API is an [*Error] with the status and the
//     body of the answer.
//
// The reasons are in docs/concepts/ai/elevenlabs.md.
package elevenlabs

import (
	"cmp"
	"net/http"
	"os"
	"strings"

	"github.com/sonnes/gopi/pkg/ai"
)

// DefaultBaseURL is the base URL of the ElevenLabs API.
const DefaultBaseURL = "https://api.elevenlabs.io"

// DefaultVoice is the voice ID of a speech call that sets no
// [ai.Voice]. It is "George", a default voice of ElevenLabs.
const DefaultVoice = "JBFqnCBsd6RMkjVDRZzb"

// keyHeader is the header that carries the API key.
const keyHeader = "xi-api-key"

// providerName starts each error of the provider.
const providerName = "elevenlabs"

// The capabilities of the provider, checked at compile time.
var (
	_ ai.SpeechProvider        = (*Provider)(nil)
	_ ai.TranscriptionProvider = (*Provider)(nil)
	_ ai.ModelLister           = (*Provider)(nil)
	_ ai.KeyChecker            = (*Provider)(nil)
)

// Provider calls the ElevenLabs API. Create one with [New].
type Provider struct {
	cfg config
}

// Option configures [New].
type Option func(*config)

type config struct {
	apiKey     string
	apiKeySet  bool
	baseURL    string
	httpClient *http.Client
}

// APIKey sets the API key. If it is unset, [New] reads
// ELEVENLABS_API_KEY.
func APIKey(key string) Option {
	return func(c *config) {
		c.apiKey = key
		c.apiKeySet = true
	}
}

// BaseURL sets the base URL of the API, for a proxy, a data residency
// server, or a test server. If it is unset, the provider uses
// [DefaultBaseURL].
func BaseURL(url string) Option {
	return func(c *config) { c.baseURL = url }
}

// HTTPClient sets the HTTP client. If it is unset, the provider uses
// [http.DefaultClient].
func HTTPClient(client *http.Client) Option {
	return func(c *config) { c.httpClient = client }
}

// New returns a provider. It retries a call when the call has
// [ai.MaxRetries].
func New(opts ...Option) *Provider {
	var c config
	for _, opt := range opts {
		opt(&c)
	}
	if !c.apiKeySet {
		c.apiKey = os.Getenv("ELEVENLABS_API_KEY")
	}
	// The paths start with a slash, so a trailing slash would double it.
	c.baseURL = strings.TrimSuffix(cmp.Or(c.baseURL, DefaultBaseURL), "/")
	c.httpClient = cmp.Or(c.httpClient, http.DefaultClient)
	return &Provider{cfg: c}
}
