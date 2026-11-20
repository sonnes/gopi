// Package higgsfield is the provider for the Higgsfield API. It
// generates images with the SOUL models. It implements
// [ai.ImageProvider] and [ai.ModelLister]. It has no text generation.
//
// The API is asynchronous. A call submits a request, reads its status
// until the status is final, and then downloads each image.
//
// A caller must obey these rules:
//
//   - The key is one string, the key ID and the key secret with a colon
//     between them: "id:secret".
//   - [ai.Character] is the ID of a Soul ID character that the owner
//     trained with Higgsfield.
//   - A call waits for its images. It can take minutes. Give the call a
//     context with the deadline that the caller can accept.
//   - A failed answer of the API is an [*Error] with the status and the
//     body of the answer.
//
// The reasons are in docs/concepts/ai/higgsfield.md.
package higgsfield

import (
	"cmp"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/sonnes/gopi/pkg/ai"
)

// DefaultBaseURL is the base URL of the Higgsfield API.
const DefaultBaseURL = "https://api.higgsfield.ai"

// keyEnv is the environment variable that holds the key. The Python SDK
// of Higgsfield reads the same variable.
const keyEnv = "HF_KEY"

// keyScheme starts the value of the Authorization header.
const keyScheme = "Key "

// providerName starts each error of the provider.
const providerName = "higgsfield"

const (
	// pollInterval is the wait between two status reads. The polling
	// guide of Higgsfield starts with two seconds.
	pollInterval = 2 * time.Second
	// pollTimeout is the longest wait for a final status. After it, the
	// call returns an error.
	pollTimeout = 5 * time.Minute
)

// The capabilities of the provider, checked at compile time.
var (
	_ ai.ImageProvider = (*Provider)(nil)
	_ ai.ModelLister   = (*Provider)(nil)
)

// Provider calls the Higgsfield API. Create one with [New].
type Provider struct {
	cfg config
}

// Option configures [New].
type Option func(*config)

type config struct {
	apiKey       string
	apiKeySet    bool
	baseURL      string
	httpClient   *http.Client
	pollInterval time.Duration
	pollTimeout  time.Duration
}

// APIKey sets the key as "id:secret". If it is unset, [New] reads
// HF_KEY.
func APIKey(key string) Option {
	return func(c *config) {
		c.apiKey = key
		c.apiKeySet = true
	}
}

// BaseURL sets the base URL of the API, for a proxy or a test server.
// If it is unset, the provider uses [DefaultBaseURL]. The status and
// cancel URLs come from the API, so this option does not change them.
func BaseURL(url string) Option {
	return func(c *config) { c.baseURL = url }
}

// HTTPClient sets the HTTP client. If it is unset, the provider uses
// [http.DefaultClient].
func HTTPClient(client *http.Client) Option {
	return func(c *config) { c.httpClient = client }
}

// New returns a provider. It retries a submit or a status read when the
// call has [ai.MaxRetries].
func New(opts ...Option) *Provider {
	var c config
	for _, opt := range opts {
		opt(&c)
	}
	if !c.apiKeySet {
		c.apiKey = os.Getenv(keyEnv)
	}
	// The paths start with a slash, so a trailing slash would double it.
	c.baseURL = strings.TrimSuffix(cmp.Or(c.baseURL, DefaultBaseURL), "/")
	c.httpClient = cmp.Or(c.httpClient, http.DefaultClient)
	c.pollInterval = cmp.Or(c.pollInterval, pollInterval)
	c.pollTimeout = cmp.Or(c.pollTimeout, pollTimeout)
	return &Provider{cfg: c}
}
