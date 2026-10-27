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
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/oauth"
	"github.com/sonnes/gopi/pkg/ai/openai"
)

// DefaultBaseURL is the base URL of the Responses route of the plan.
const DefaultBaseURL = "https://api.openai.com/v1"

// maxErrorBody is the most bytes of an error body that an error holds.
const maxErrorBody = 1024

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

// efforts maps a reasoning effort of the model list to an
// [ai.ThinkingLevel]. An effort with no level, such as "minimal", is
// not in the list of the model.
var efforts = map[string]ai.ThinkingLevel{
	"none":   ai.ThinkingOff,
	"low":    ai.ThinkingLow,
	"medium": ai.ThinkingMedium,
	"high":   ai.ThinkingHigh,
	"xhigh":  ai.ThinkingXHigh,
	"max":    ai.ThinkingMax,
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
// gopi value, such as "ultra", is not in the model.
func (p *Provider) ListModels(ctx context.Context) ([]ai.Model, error) {
	body, err := p.get(ctx, "/models")
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
		var levels []ai.ThinkingLevel
		for _, l := range m.ReasoningLevels {
			level, ok := efforts[l.Effort]
			if ok && !slices.Contains(levels, level) {
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

// get sends a GET request for path and returns the body of a 200
// response.
func (p *Provider) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	res, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", res.Status, truncate(body))
	}
	return body, nil
}

// truncate returns at most [maxErrorBody] bytes of body as a string.
func truncate(body []byte) string {
	if len(body) > maxErrorBody {
		body = body[:maxErrorBody]
	}
	return string(body)
}
