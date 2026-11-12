// Package chatgpt is the provider for the ChatGPT plan of a user. It
// signs in with Sign in with ChatGPT, keeps the OAuth tokens of the
// account, and calls the Responses API through the provider of package
// openai with [openai.ChatGPTPlan]. It implements [ai.TextProvider],
// [ai.ObjectProvider], and [ai.ModelLister].
//
// A caller must obey these rules:
//
//   - Plan usage is for open-source apps and for apps that run on the
//     host of the user. A hosted app needs the approval of OpenAI.
//   - A caller keeps the [Account] of a sign-in and gives it to the
//     next sign-in. A sign-in without it registers the app again, and
//     each registration stays on the account of the user.
//   - The refresh token rotates on each refresh. Two processes that
//     share an account share one file through [FileSource], which
//     refreshes under a lock on the file.
//   - The package has no models. A caller lists them with
//     [Provider.ListModels], because the models depend on the account.
//   - An API error wraps openai.Error. A caller reads the status code
//     with errors.As.
//
// The reasons are in docs/concepts/ai/chatgpt.md.
package chatgpt

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/sonnes/gopi/internal/httpapi"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/oauth"
	"github.com/sonnes/gopi/pkg/ai/openai"
)

// DefaultBaseURL is the base URL of the Responses route of the plan.
const DefaultBaseURL = "https://api.openai.com/v1"

// listedVisibility is the visibility of a model that the model picker
// of ChatGPT shows. A model with another visibility is not for a user.
const listedVisibility = "list"

// The capabilities of the provider, checked at compile time.
var (
	_ ai.TextProvider   = (*Provider)(nil)
	_ ai.ObjectProvider = (*Provider)(nil)
	_ ai.ModelLister    = (*Provider)(nil)
)

// Provider calls the Responses API on the ChatGPT plan of one account.
// Create one with [New].
type Provider struct {
	responses *openai.Provider
	client    *http.Client
	baseURL   string
}

// Option configures [New].
type Option func(*config)

type config struct {
	baseURL string
	base    http.RoundTripper
}

// BaseURL sets the base URL of the API, for a test server. If it is
// unset, the provider uses [DefaultBaseURL].
func BaseURL(url string) Option {
	return func(c *config) { c.baseURL = url }
}

// Transport sets the transport under the OAuth transport, for a test
// that records and replays traffic. If it is unset, the provider uses
// [http.DefaultTransport].
func Transport(rt http.RoundTripper) Option {
	return func(c *config) { c.base = rt }
}

// New returns a provider that gets its token from src, such as a
// [FileSource].
func New(src oauth.Source, opts ...Option) *Provider {
	var c config
	for _, opt := range opts {
		opt(&c)
	}
	// The paths start with a slash, so a trailing slash would double it.
	baseURL := strings.TrimSuffix(cmp.Or(c.baseURL, DefaultBaseURL), "/")
	client := &http.Client{
		Transport: &oauth.Transport{
			Source: src,
			Base:   c.base,
		},
	}
	// The empty key stops the SDK from reading OPENAI_API_KEY. The OAuth
	// transport sets the Authorization header.
	responses := openai.New(
		openai.APIKey(""),
		openai.BaseURL(baseURL),
		openai.HTTPClient(client),
		openai.ChatGPTPlan(),
	)
	return &Provider{
		responses: responses,
		client:    client,
		baseURL:   baseURL,
	}
}

// StreamText streams one response on the plan.
func (p *Provider) StreamText(
	ctx context.Context,
	model ai.Model,
	messages []ai.Message,
	opts ai.Options,
) *ai.EventStream {
	return p.responses.StreamText(ctx, model, messages, opts)
}

// GenerateObject asks for a JSON object that matches schema. It reads
// the response from a stream, because the route serves only a stream.
func (p *Provider) GenerateObject(
	ctx context.Context,
	model ai.Model,
	messages []ai.Message,
	schema *jsonschema.Schema,
	opts ai.Options,
) (*ai.ObjectResponse, error) {
	return p.responses.GenerateObject(ctx, model, messages, schema, opts)
}

// listResponse is the body of GET /models on the plan.
type listResponse struct {
	Models []struct {
		Slug            string   `json:"slug"`
		DisplayName     string   `json:"display_name"`
		Visibility      string   `json:"visibility"`
		SupportedInAPI  bool     `json:"supported_in_api"`
		ContextWindow   int      `json:"context_window"`
		InputModalities []string `json:"input_modalities"`
		ReasoningLevels []struct {
			Effort string `json:"effort"`
		} `json:"supported_reasoning_levels"`
	} `json:"models"`
}

// modalities maps an input modality of the model list to an
// [ai.Modality].
var modalities = map[string]ai.Modality{
	"text":  ai.ModalityText,
	"image": ai.ModalityImage,
}

// ListModels lists the models that the account can use on the route.
// It skips a model that the model picker hides, and a model that the
// API does not serve. A reasoning effort or an input modality with no
// gopi value, such as "ultra", is not in the model. The thinking levels
// of a model are in the order of [ai.ThinkingLevels].
func (p *Provider) ListModels(ctx context.Context) ([]ai.Model, error) {
	body, err := httpapi.Get(ctx, p.client, p.baseURL+"/models", "")
	if err != nil {
		return nil, fmt.Errorf("chatgpt: list models: %w", err)
	}
	var list listResponse
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("chatgpt: list models: %w", err)
	}

	var models []ai.Model
	for _, m := range list.Models {
		listed := m.Visibility == listedVisibility && m.SupportedInAPI
		if !listed {
			continue
		}
		var efforts []string
		for _, l := range m.ReasoningLevels {
			efforts = append(efforts, l.Effort)
		}
		var levels []ai.ThinkingLevel
		for _, level := range ai.ThinkingLevels() {
			if slices.Contains(efforts, level.Effort()) {
				levels = append(levels, level)
			}
		}
		var input []ai.Modality
		for _, name := range m.InputModalities {
			if mod, ok := modalities[name]; ok {
				input = append(input, mod)
			}
		}
		models = append(models, ai.Model{
			ID:             m.Slug,
			Name:           m.DisplayName,
			ContextWindow:  m.ContextWindow,
			Input:          input,
			Reasoning:      len(levels) > 0,
			ThinkingLevels: levels,
		})
	}
	return models, nil
}
