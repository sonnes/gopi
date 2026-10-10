// Package anthropic is the provider for the Anthropic Messages API. It
// implements [ai.TextProvider], [ai.ObjectProvider], [ai.TokenCounter],
// and [ai.ModelLister].
//
// A caller must obey these rules:
//
//   - Prompt caching is on unless [ai.CacheRetention] is [ai.CacheNone].
//     The provider marks the system prompt, the last tool, and the last
//     user or system message. A caller puts the stable part of a prompt
//     first, so the prefix stays the same between calls.
//   - A model that takes no system messages gets each one as a
//     system-reminder in a user message.
//   - A model that manages its effort records the effort on each
//     assistant message, in [ai.Message.Effort]. A caller keeps the
//     message as it is, so a change of level keeps the cache.
//   - A tool input streams as the model writes it. An input that stops
//     at the output cap can be invalid JSON. [tool.New] reports it
//     as an error result, so the model can retry.
//   - An API error wraps [Error]. A caller reads the status code with
//     errors.As.
//
// The reasons are in docs/concepts/ai/provider.md.
package anthropic

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"
	"github.com/google/jsonschema-go/jsonschema"

	"github.com/sonnes/gopi/internal/retry"
	"github.com/sonnes/gopi/pkg/ai"
)

// The capabilities of the provider, checked at compile time.
var (
	_ ai.TextProvider   = (*Provider)(nil)
	_ ai.ObjectProvider = (*Provider)(nil)
	_ ai.TokenCounter   = (*Provider)(nil)
)

// Error is an API error from Anthropic. A caller reads the status code
// and the request ID from it with errors.As.
type Error = sdk.Error

// The models that this package serves. Each one carries its output cap,
// so a call without [ai.MaxTokens] gets the full cap. A model with no
// cap, such as a model that a caller passes by ID, gets 64000 tokens.
//
// If a call sets no [ai.Thinking] level, [Fable5_1], [Opus5_5], [Opus5],
// and [Sonnet5_5] use the effort high. Their IDs on OpenRouter get the
// same default. Another model uses its own default.
//
// The values come from the models overview and the pricing page of the
// Claude docs.
// CacheWrite is the price of the 5-minute cache. CacheWriteLong is the
// price of the 1-hour cache, two times the input price.
var (
	Fable5_1 = ai.Model{
		ID:            "claude-fable-5-1",
		Name:          "Claude Fable 5.1",
		MaxTokens:     128_000,
		ContextWindow: 1_000_000,
		Input:         claudeInput,
		Reasoning:     true,
		// Thinking is always on, so the model rejects off.
		ThinkingLevels: []ai.ThinkingLevel{
			ai.ThinkingLow,
			ai.ThinkingMedium,
			ai.ThinkingHigh,
			ai.ThinkingXHigh,
			ai.ThinkingMax,
		},
		Pricing: ai.Pricing{Input: 10, Output: 50, CacheRead: 0.25, CacheWrite: 12.5, CacheWriteLong: 20},
	}
	Opus5_5 = ai.Model{
		ID:            "claude-opus-5-5",
		Name:          "Claude Opus 5.5",
		MaxTokens:     128_000,
		ContextWindow: 1_000_000,
		Input:         claudeInput,
		Reasoning:     true,
		// Adaptive thinking is always on, so the model rejects off.
		ThinkingLevels: []ai.ThinkingLevel{
			ai.ThinkingLow,
			ai.ThinkingMedium,
			ai.ThinkingHigh,
			ai.ThinkingXHigh,
			ai.ThinkingMax,
		},
		Pricing: ai.Pricing{Input: 4, Output: 20, CacheRead: 0.2, CacheWrite: 5, CacheWriteLong: 8},
	}
	Opus5 = ai.Model{
		ID:            "claude-opus-5",
		Name:          "Claude Opus 5",
		MaxTokens:     128_000,
		ContextWindow: 1_000_000,
		Input:         claudeInput,
		Reasoning:     true,
		// The provider always sends adaptive thinking, so that a change
		// of effort keeps the cache. So off is not a level here.
		ThinkingLevels: []ai.ThinkingLevel{
			ai.ThinkingLow,
			ai.ThinkingMedium,
			ai.ThinkingHigh,
			ai.ThinkingXHigh,
			ai.ThinkingMax,
		},
		Pricing: ai.Pricing{Input: 5, Output: 25, CacheRead: 0.5, CacheWrite: 6.25, CacheWriteLong: 10},
	}
	Sonnet5_5 = ai.Model{
		ID:            "claude-sonnet-5-5",
		Name:          "Claude Sonnet 5.5",
		MaxTokens:     128_000,
		ContextWindow: 1_000_000,
		Input:         claudeInput,
		Reasoning:     true,
		// The model rejects disabled thinking. Its way to turn thinking
		// off has no level here.
		ThinkingLevels: []ai.ThinkingLevel{
			ai.ThinkingLow,
			ai.ThinkingMedium,
			ai.ThinkingHigh,
			ai.ThinkingXHigh,
			ai.ThinkingMax,
		},
		Pricing: ai.Pricing{Input: 2, Output: 10, CacheRead: 0.2, CacheWrite: 2.5, CacheWriteLong: 4},
	}
	Sonnet5 = ai.Model{
		ID:            "claude-sonnet-5",
		Name:          "Claude Sonnet 5",
		MaxTokens:     128_000,
		ContextWindow: 1_000_000,
		Input:         claudeInput,
		Reasoning:     true,
		ThinkingLevels: []ai.ThinkingLevel{
			ai.ThinkingLow,
			ai.ThinkingMedium,
			ai.ThinkingHigh,
			ai.ThinkingXHigh,
			ai.ThinkingMax,
		},
		Pricing: ai.Pricing{Input: 2, Output: 10, CacheRead: 0.2, CacheWrite: 2.5, CacheWriteLong: 4},
	}
	// Haiku4_5 thinks only with a token budget, and the provider sends
	// effort levels. So the provider can only turn its thinking off.
	Haiku4_5 = ai.Model{
		ID:             "claude-haiku-4-5",
		Name:           "Claude Haiku 4.5",
		MaxTokens:      64_000,
		ContextWindow:  200_000,
		Input:          claudeInput,
		ThinkingLevels: []ai.ThinkingLevel{ai.ThinkingOff},
		Pricing:        ai.Pricing{Input: 1, Output: 5, CacheRead: 0.1, CacheWrite: 1.25, CacheWriteLong: 2},
	}
)

// claudeInput is what every Claude model reads.
var claudeInput = []ai.Modality{ai.ModalityText, ai.ModalityImage, ai.ModalityPDF}

// Models returns the models that this package serves. The default model
// comes first, so a caller that must pick one takes the first.
func Models() []ai.Model {
	return []ai.Model{
		Sonnet5_5,
		Fable5_1,
		Opus5_5,
		Opus5,
		Sonnet5,
		Haiku4_5,
	}
}

// Provider calls the Anthropic Messages API. Create one with [New].
type Provider struct {
	client sdk.Client
}

// Option configures [New].
type Option func(*config)

type config struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// APIKey sets the API key. If it is unset, the SDK reads
// ANTHROPIC_API_KEY.
func APIKey(key string) Option {
	return func(c *config) { c.apiKey = key }
}

// BaseURL sets the base URL of the API, for a proxy or a test server.
// If it is unset, the SDK reads ANTHROPIC_BASE_URL. If that is unset
// too, the SDK uses https://api.anthropic.com.
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
func New(opts ...Option) *Provider {
	var c config
	for _, opt := range opts {
		opt(&c)
	}

	clientOpts := []option.RequestOption{
		option.WithMaxRetries(0),
	}
	if c.apiKey != "" {
		clientOpts = append(clientOpts, option.WithAPIKey(c.apiKey))
	}
	if c.baseURL != "" {
		clientOpts = append(clientOpts, option.WithBaseURL(c.baseURL))
	}
	if c.httpClient != nil {
		clientOpts = append(clientOpts, option.WithHTTPClient(c.httpClient))
	}

	return &Provider{client: sdk.NewClient(clientOpts...)}
}

// OnPayload sets a hook that reads or changes the request of a stream
// or object call before the provider sends it. An error from the hook
// stops the call. The hook runs once for each call, before the first
// attempt.
var OnPayload = ai.NewOption[func(params *sdk.MessageNewParams) error]()

// applyHook runs the [OnPayload] hook on the request, if opts has one.
func applyHook(req *request, opts ai.Options) error {
	hook, ok := ai.GetOption(opts, OnPayload)
	if !ok || hook == nil {
		return nil
	}
	if err := hook(&req.params); err != nil {
		return fmt.Errorf("anthropic: payload hook: %w", err)
	}
	return nil
}

// requestOptions returns one header for each beta, and the headers of
// [ai.Headers].
func requestOptions(betas []string, opts ai.Options) []option.RequestOption {
	var reqOpts []option.RequestOption
	for _, beta := range betas {
		reqOpts = append(reqOpts, option.WithHeaderAdd("anthropic-beta", beta))
	}
	headers, _ := ai.GetOption(opts, ai.Headers)
	for name, value := range headers {
		reqOpts = append(reqOpts, option.WithHeader(name, value))
	}
	return reqOpts
}

// StreamText streams one response. Each content block pushes its start,
// delta, and end events with its index. The final message comes from the
// accumulator of the SDK, so it holds the signed thinking blocks and the
// usage.
func (p *Provider) StreamText(
	ctx context.Context,
	model ai.Model,
	messages []ai.Message,
	opts ai.Options,
) *ai.EventStream {
	req, err := buildRequest(model, messages, opts)
	if err != nil {
		return ai.ErrStream(err)
	}
	if err := applyHook(&req, opts); err != nil {
		return ai.ErrStream(err)
	}
	reqOpts := requestOptions(req.betas, opts)

	return ai.NewEventStream(func(push func(ai.Event)) (*ai.Message, error) {
		// blockTypes keeps the type of each block by index, because the
		// stop event carries the index and nothing else.
		var blockTypes []string

		onChunk := func(chunk sdk.MessageStreamEventUnion, acc *sdk.Message) {
			index := int(chunk.Index)
			switch chunk.Type {
			case "message_start":
				push(ai.Event{Type: ai.EventStart})

			case "content_block_start":
				blockTypes = append(blockTypes, chunk.ContentBlock.Type)
				if eventType, ok := startEvents[chunk.ContentBlock.Type]; ok {
					push(ai.Event{Type: eventType, ContentIndex: index})
				}

			case "content_block_delta":
				switch chunk.Delta.Type {
				case "text_delta":
					push(ai.Event{
						Type:         ai.EventTextDelta,
						ContentIndex: index,
						Delta:        chunk.Delta.Text,
					})
				case "thinking_delta":
					push(ai.Event{
						Type:         ai.EventThinkDelta,
						ContentIndex: index,
						Delta:        chunk.Delta.Thinking,
					})
				case "input_json_delta":
					push(ai.Event{
						Type:         ai.EventToolDelta,
						ContentIndex: index,
						Delta:        chunk.Delta.PartialJSON,
					})
				}

			case "content_block_stop":
				if index >= len(blockTypes) || index >= len(acc.Content) {
					return
				}
				block := acc.Content[index]
				switch blockTypes[index] {
				case "text":
					push(ai.Event{
						Type:         ai.EventTextEnd,
						ContentIndex: index,
						Content:      block.Text,
					})
				case "thinking":
					push(ai.Event{Type: ai.EventThinkEnd, ContentIndex: index})
				case "tool_use":
					call := toolCall(block)
					push(ai.Event{
						Type:         ai.EventToolEnd,
						ContentIndex: index,
						ToolCall:     &call,
					})
				}
			}
		}

		stream, err := p.openStream(ctx, req.params, retry.PolicyOf(opts), reqOpts)
		if err != nil {
			return nil, err
		}
		acc, err := consume(stream, onChunk)
		if err != nil {
			return nil, err
		}

		return buildMessage(model, acc, req.effort)
	})
}

// consume folds every chunk of stream into one message. It calls fn
// after each chunk, with the message so far.
func consume(
	stream *ssestream.Stream[sdk.MessageStreamEventUnion],
	fn func(chunk sdk.MessageStreamEventUnion, acc *sdk.Message),
) (sdk.Message, error) {
	var acc sdk.Message
	for stream.Next() {
		chunk := stream.Current()
		if err := acc.Accumulate(chunk); err != nil {
			return acc, fmt.Errorf("anthropic: %w", err)
		}
		fn(chunk, &acc)
	}

	if err := stream.Err(); err != nil && !errors.Is(err, io.EOF) {
		return acc, fmt.Errorf("anthropic: %w", err)
	}

	return acc, nil
}

// startEvents maps a content block type to its start event.
var startEvents = map[string]ai.EventType{
	"text":     ai.EventTextStart,
	"thinking": ai.EventThinkStart,
	"tool_use": ai.EventToolStart,
}

// GenerateObject asks for a JSON object that matches schema, through
// structured outputs. The text of the response is the object. The call
// streams, because the SDK rejects a call without a stream at the full
// output cap of the model.
func (p *Provider) GenerateObject(
	ctx context.Context,
	model ai.Model,
	messages []ai.Message,
	schema *jsonschema.Schema,
	opts ai.Options,
) (*ai.ObjectResponse, error) {
	req, err := buildObjectRequest(model, messages, schema, opts)
	if err != nil {
		return nil, err
	}
	if err := applyHook(&req, opts); err != nil {
		return nil, err
	}

	stream, err := p.openStream(
		ctx,
		req.params,
		retry.PolicyOf(opts),
		requestOptions(req.betas, opts),
	)
	if err != nil {
		return nil, err
	}
	resp, err := consume(stream, func(sdk.MessageStreamEventUnion, *sdk.Message) {})
	if err != nil {
		return nil, err
	}

	var raw string
	for _, block := range resp.Content {
		if block.Type == "text" {
			raw += block.Text
		}
	}
	if raw == "" {
		return nil, fmt.Errorf("anthropic: no object in response, stop reason %q", resp.StopReason)
	}

	return &ai.ObjectResponse{
		Raw:   raw,
		Usage: mapUsage(resp.Usage),
	}, nil
}

// CountTokens counts the input tokens of a call. It sends the same
// messages, system prompt, tools, and thinking config as a call.
func (p *Provider) CountTokens(
	ctx context.Context,
	model ai.Model,
	messages []ai.Message,
	opts ai.Options,
) (*ai.TokenCount, error) {
	params, betas, err := buildCountTokensParams(model, messages, opts)
	if err != nil {
		return nil, err
	}

	resp, err := p.client.Messages.CountTokens(ctx, params, requestOptions(betas, opts)...)
	if err != nil {
		return nil, fmt.Errorf("anthropic: %w", err)
	}

	return &ai.TokenCount{Total: int(resp.InputTokens)}, nil
}
