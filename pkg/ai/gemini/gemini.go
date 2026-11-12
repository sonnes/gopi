// Package gemini is the provider for the Gemini Developer API. It
// implements [ai.TextProvider] and [ai.ObjectProvider] on the
// Interactions API, and [ai.ImageProvider], [ai.ModelLister], and
// [ai.KeyChecker] on the models API. It does not implement
// [ai.TokenCounter], because the Interactions API has no token count.
//
// A caller must obey these rules:
//
//   - The API stores nothing. Each request sends the whole history with
//     store set to false. A thinking block carries the thought signature
//     of the model. A caller sends the assistant message back unchanged,
//     so the model keeps its reasoning between tool calls.
//   - The input has no system role. Each system message goes as a
//     system-reminder in a user turn.
//   - A failed answer of the API is an [*Error] with the status and
//     the message of the answer.
//
// The reasons are in docs/concepts/ai/gemini.md.
package gemini

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"google.golang.org/genai"

	"github.com/sonnes/gopi/pkg/ai"
)

var (
	_ ai.TextProvider   = (*Provider)(nil)
	_ ai.ObjectProvider = (*Provider)(nil)
)

// Provider calls the Gemini Developer API. Create one with [New].
type Provider struct {
	client *genai.Client
	// err is the error of [genai.NewClient]. Each call returns it.
	err error
}

// Option configures [New].
type Option func(*config)

type config struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// APIKey sets the API key. If it is unset, the SDK reads GOOGLE_API_KEY,
// then GEMINI_API_KEY.
func APIKey(key string) Option {
	return func(c *config) { c.apiKey = key }
}

// BaseURL sets the base URL of the API, for a proxy or a test server.
// If it is unset, the SDK uses https://generativelanguage.googleapis.com.
func BaseURL(url string) Option {
	return func(c *config) { c.baseURL = url }
}

// HTTPClient sets the HTTP client. A test passes a client that records
// and replays traffic. If it is unset, the SDK uses its own client.
func HTTPClient(client *http.Client) Option {
	return func(c *config) { c.httpClient = client }
}

// New returns a provider. The SDK does not retry. The provider retries
// a call when it has [ai.MaxRetries], so the wait has a cap.
//
// If the SDK cannot make its client, for example because no key is set,
// each call of the provider returns the error.
func New(opts ...Option) *Provider {
	var c config
	for _, opt := range opts {
		opt(&c)
	}

	cc := &genai.ClientConfig{
		APIKey:     c.apiKey,
		Backend:    genai.BackendGeminiAPI,
		HTTPClient: c.httpClient,
		HTTPOptions: genai.HTTPOptions{
			BaseURL: c.baseURL,
		},
	}
	client, err := genai.NewClient(context.Background(), cc)
	if err != nil {
		err = fmt.Errorf("gemini: new client: %w", err)
	}
	return &Provider{
		client: client,
		err:    err,
	}
}

// StreamText streams one response. Each step of the interaction pushes
// its start, delta, and end events with its index in the final message.
func (p *Provider) StreamText(
	ctx context.Context,
	model ai.Model,
	messages []ai.Message,
	opts ai.Options,
) *ai.EventStream {
	if p.err != nil {
		return ai.ErrStream(p.err)
	}
	body, err := buildStreamRequest(model, messages, opts)
	if err != nil {
		return ai.ErrStream(err)
	}

	return ai.NewEventStream(func(push func(ai.Event)) (*ai.Message, error) {
		res, err := p.interact(ctx, body, opts)
		if err != nil {
			return nil, err
		}
		events := res.InteractionSSEStreamEvent
		if events == nil {
			return nil, errors.New("gemini: the response is not a stream")
		}
		defer events.Close()

		s := newStreamState(push)
		for events.Next() {
			done, err := s.handle(events.Value())
			if err != nil {
				return nil, err
			}
			if done {
				return s.message(model)
			}
		}
		if err := events.Err(); err != nil {
			return nil, fmt.Errorf("gemini: stream: %w", err)
		}
		return nil, errors.New("gemini: stream ended before the interaction completed")
	})
}
