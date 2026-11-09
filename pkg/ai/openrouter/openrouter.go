// Package openrouter is the provider for OpenRouter. It calls the
// Responses API of OpenRouter through the provider of package openai,
// and lists the models of OpenRouter with their limits and prices. It
// implements [ai.TextProvider], [ai.ObjectProvider], and
// [ai.ModelLister].
//
// A caller must obey these rules:
//
//   - The package has no models. A caller lists them with
//     [Provider.ListModels], or with the FetchModels method of a
//     catalog.
//   - The call options of package openai apply, such as
//     openai.PromptCacheKey and openai.OnPayload.
//   - An API error wraps openai.Error. A caller reads the status code
//     with errors.As.
//
// The reasons are in docs/concepts/ai/host-provider.md.
package openrouter

import (
	"cmp"
	"context"
	"net/http"
	"os"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/openai"
)

// DefaultBaseURL is the base URL of the API of OpenRouter.
const DefaultBaseURL = "https://openrouter.ai/api/v1"

// The capabilities of the provider, checked at compile time.
var (
	_ ai.TextProvider   = (*Provider)(nil)
	_ ai.ObjectProvider = (*Provider)(nil)
	_ ai.ModelLister    = (*Provider)(nil)
)

// Provider calls OpenRouter. Create one with [New].
type Provider struct {
	responses *openai.Provider
	cfg       config
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
// OPENROUTER_API_KEY. The provider never reads OPENAI_API_KEY.
func APIKey(key string) Option {
	return func(c *config) {
		c.apiKey = key
		c.apiKeySet = true
	}
}

// BaseURL sets the base URL of the API, for a proxy or a test server. If
// it is unset, the provider uses [DefaultBaseURL].
func BaseURL(url string) Option {
	return func(c *config) { c.baseURL = url }
}

// HTTPClient sets the HTTP client. If it is unset, the provider uses
// [http.DefaultClient].
func HTTPClient(client *http.Client) Option {
	return func(c *config) { c.httpClient = client }
}

// New returns a provider. It retries a call when the call has
// [ai.MaxRetries], as the provider of package openai does.
func New(opts ...Option) *Provider {
	var c config
	for _, opt := range opts {
		opt(&c)
	}
	if !c.apiKeySet {
		c.apiKey = os.Getenv("OPENROUTER_API_KEY")
	}
	// The paths start with a slash, so a trailing slash would double it.
	c.baseURL = strings.TrimSuffix(cmp.Or(c.baseURL, DefaultBaseURL), "/")

	// The key and the base URL are always set, so the openai provider
	// does not read OPENAI_API_KEY or OPENAI_BASE_URL.
	responsesOpts := []openai.Option{
		openai.APIKey(c.apiKey),
		openai.BaseURL(c.baseURL),
	}
	if c.httpClient != nil {
		responsesOpts = append(responsesOpts, openai.HTTPClient(c.httpClient))
	}
	responses := openai.New(responsesOpts...)
	return &Provider{
		responses: responses,
		cfg:       c,
	}
}

// StreamText streams one response from the Responses API of
// OpenRouter.
func (p *Provider) StreamText(
	ctx context.Context,
	model ai.Model,
	messages []ai.Message,
	opts ai.Options,
) *ai.EventStream {
	return p.responses.StreamText(ctx, model, messages, opts)
}

// GenerateObject asks for a JSON object that matches schema, through
// the structured outputs of the Responses API of OpenRouter.
func (p *Provider) GenerateObject(
	ctx context.Context,
	model ai.Model,
	messages []ai.Message,
	schema *jsonschema.Schema,
	opts ai.Options,
) (*ai.ObjectResponse, error) {
	return p.responses.GenerateObject(ctx, model, messages, schema, opts)
}
