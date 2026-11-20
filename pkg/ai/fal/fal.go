// Package fal is the provider for the image models of fal.ai. It
// generates images with a sync call to fal.run. It implements
// [ai.ImageProvider], [ai.ModelLister], and [ai.KeyChecker]. It has no
// text generation.
//
// A caller must obey these rules:
//
//   - A model ID is the endpoint ID of fal, such as
//     "fal-ai/flux-2-pro". The ID contains slashes.
//   - [Provider.ListModels] returns a fixed list of text-to-image
//     endpoints. The list does not call the API.
//   - Each endpoint takes a different set of size fields. The provider
//     maps the options to the fields of the endpoint. It drops an option
//     that the endpoint does not take.
//   - A new account runs two requests at the same time. A third sync
//     call gets status 429. The provider retries it only when the call
//     sets [ai.MaxRetries].
//   - A failed answer of the API is an [*Error] with the status and the
//     body of the answer.
//
// The reasons are in docs/concepts/ai/fal.md.
package fal

import (
	"cmp"
	"net/http"
	"os"
	"strings"

	"github.com/sonnes/gopi/pkg/ai"
)

// DefaultBaseURL is the base URL of the sync calls of the models.
const DefaultBaseURL = "https://fal.run"

// DefaultAPIBaseURL is the base URL of the platform API, which gives
// the prices of the models.
const DefaultAPIBaseURL = "https://api.fal.ai"

// keyEnv is the environment variable that holds the API key.
const keyEnv = "FAL_KEY"

// keyScheme starts the value of the Authorization header.
const keyScheme = "Key "

// providerName starts each error of the provider.
const providerName = "fal"

// The capabilities of the provider, checked at compile time.
var (
	_ ai.ImageProvider = (*Provider)(nil)
	_ ai.ModelLister   = (*Provider)(nil)
	_ ai.KeyChecker    = (*Provider)(nil)
)

// Provider calls the fal.ai API. Create one with [New].
type Provider struct {
	cfg config
}

// Option configures [New].
type Option func(*config)

type config struct {
	apiKey     string
	apiKeySet  bool
	baseURL    string
	apiBaseURL string
	httpClient *http.Client
}

// APIKey sets the API key. If it is unset, [New] reads FAL_KEY.
func APIKey(key string) Option {
	return func(c *config) {
		c.apiKey = key
		c.apiKeySet = true
	}
}

// BaseURL sets the base URL of the sync calls, for a proxy or a test
// server. If it is unset, the provider uses [DefaultBaseURL].
func BaseURL(url string) Option {
	return func(c *config) { c.baseURL = url }
}

// APIBaseURL sets the base URL of the platform API, for a proxy or a
// test server. If it is unset, the provider uses [DefaultAPIBaseURL].
func APIBaseURL(url string) Option {
	return func(c *config) { c.apiBaseURL = url }
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
		c.apiKey = os.Getenv(keyEnv)
	}
	// The paths start with a slash, so a trailing slash would double it.
	c.baseURL = strings.TrimSuffix(cmp.Or(c.baseURL, DefaultBaseURL), "/")
	c.apiBaseURL = strings.TrimSuffix(cmp.Or(c.apiBaseURL, DefaultAPIBaseURL), "/")
	c.httpClient = cmp.Or(c.httpClient, http.DefaultClient)
	return &Provider{cfg: c}
}
