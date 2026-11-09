// Package fireworks is the provider for Fireworks. It calls the Chat
// Completions API of Fireworks through the provider of package
// openaichat, and lists the models of a Fireworks account with their
// context windows and input modalities. It implements
// [ai.TextProvider], [ai.ObjectProvider], and [ai.ModelLister].
//
// A caller must obey these rules:
//
//   - The package has no models. A caller lists them with
//     [Provider.ListModels], or with the FetchModels method of a
//     catalog.
//   - The list has no output cap and no prices. A listed model has
//     MaxTokens zero, so Fireworks uses its default output cap. A
//     caller that needs a longer answer than that cap sets
//     ai.MaxTokens on the call.
//   - The call options of package openaichat apply, such as
//     openaichat.OnPayload.
//   - An API error wraps openaichat.Error. A caller reads the status
//     code with errors.As.
//
// The reasons are in docs/concepts/ai/host-provider.md.
package fireworks

import (
	"cmp"
	"context"
	"net/http"
	"os"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/openaichat"
)

// DefaultBaseURL is the root URL of the API of Fireworks. The provider
// calls the Chat Completions API under /inference/v1, and lists the
// models under /v1.
const DefaultBaseURL = "https://api.fireworks.ai"

// DefaultAccount is the Fireworks account of the public models.
const DefaultAccount = "fireworks"

// inferencePath is the path of the Chat Completions API under the root
// URL.
const inferencePath = "/inference/v1"

// The capabilities of the provider, checked at compile time.
var (
	_ ai.TextProvider   = (*Provider)(nil)
	_ ai.ObjectProvider = (*Provider)(nil)
	_ ai.ModelLister    = (*Provider)(nil)
)

// Provider calls Fireworks. Create one with [New].
type Provider struct {
	chat *openaichat.Provider
	cfg  config
}

// Option configures [New].
type Option func(*config)

type config struct {
	apiKey     string
	apiKeySet  bool
	baseURL    string
	account    string
	httpClient *http.Client
}

// APIKey sets the API key. If it is unset, [New] reads
// FIREWORKS_API_KEY. The provider never reads OPENAI_API_KEY.
func APIKey(key string) Option {
	return func(c *config) {
		c.apiKey = key
		c.apiKeySet = true
	}
}

// BaseURL sets the root URL of the API, for a proxy or a test server.
// If it is unset, the provider uses [DefaultBaseURL].
func BaseURL(url string) Option {
	return func(c *config) { c.baseURL = url }
}

// Account sets the Fireworks account whose models
// [Provider.ListModels] lists, such as the account of a fine-tuned
// model. If it is unset, the provider uses [DefaultAccount].
func Account(id string) Option {
	return func(c *config) { c.account = id }
}

// HTTPClient sets the HTTP client. If it is unset, the provider uses
// [http.DefaultClient].
func HTTPClient(client *http.Client) Option {
	return func(c *config) { c.httpClient = client }
}

// New returns a provider. It retries a call when the call has
// [ai.MaxRetries], as the provider of package openaichat does.
func New(opts ...Option) *Provider {
	var c config
	for _, opt := range opts {
		opt(&c)
	}
	if !c.apiKeySet {
		c.apiKey = os.Getenv("FIREWORKS_API_KEY")
	}
	// The paths start with a slash, so a trailing slash would double it.
	c.baseURL = strings.TrimSuffix(cmp.Or(c.baseURL, DefaultBaseURL), "/")
	c.account = cmp.Or(c.account, DefaultAccount)

	// The key and the base URL are always set, so the openaichat
	// provider does not read OPENAI_API_KEY or OPENAI_BASE_URL.
	chatOpts := []openaichat.Option{
		openaichat.APIKey(c.apiKey),
		openaichat.BaseURL(c.baseURL + inferencePath),
	}
	if c.httpClient != nil {
		chatOpts = append(chatOpts, openaichat.HTTPClient(c.httpClient))
	}
	return &Provider{
		chat: openaichat.New(chatOpts...),
		cfg:  c,
	}
}

// StreamText streams one response from the Chat Completions API of
// Fireworks.
func (p *Provider) StreamText(
	ctx context.Context,
	model ai.Model,
	messages []ai.Message,
	opts ai.Options,
) *ai.EventStream {
	return p.chat.StreamText(ctx, model, messages, opts)
}

// GenerateObject asks for a JSON object that matches schema, through
// the structured outputs of the Chat Completions API of Fireworks.
func (p *Provider) GenerateObject(
	ctx context.Context,
	model ai.Model,
	messages []ai.Message,
	schema *jsonschema.Schema,
	opts ai.Options,
) (*ai.ObjectResponse, error) {
	return p.chat.GenerateObject(ctx, model, messages, schema, opts)
}
