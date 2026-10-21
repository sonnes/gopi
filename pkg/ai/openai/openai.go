// Package openai is the provider for the OpenAI Responses API. It
// implements [ai.TextProvider], [ai.ObjectProvider], [ai.TokenCounter],
// [ai.ImageProvider], and [ai.ModelLister]. It also serves a compatible Responses API,
// such as the one of OpenRouter, through [BaseURL].
//
// A caller must obey these rules:
//
//   - The API stores nothing. A thinking block carries the encrypted
//     reasoning of the model in its signature. A caller sends the
//     assistant message back unchanged, so the model keeps its
//     reasoning between tool calls.
//   - Prompt caching is on unless [ai.CacheRetention] is [ai.CacheNone].
//     The provider marks the end of the system prompt, and the API adds
//     its own breakpoint. A caller puts the stable part of a prompt
//     first. [PromptCacheKey] routes the calls of one conversation to
//     the same cache.
//   - A model that takes no developer message in the conversation gets
//     each system message as a system-reminder in a user message.
//   - An API error wraps [Error]. A caller reads the status code with
//     errors.As.
//
// The reasons are in docs/concepts/ai/openai.md.
package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/ssestream"
	"github.com/openai/openai-go/v3/responses"

	"github.com/sonnes/gopi/internal/retry"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/catalog/models"
)

// The capabilities of the provider, checked at compile time.
var (
	_ ai.TextProvider   = (*Provider)(nil)
	_ ai.ObjectProvider = (*Provider)(nil)
	_ ai.TokenCounter   = (*Provider)(nil)
	_ ai.ImageProvider  = (*Provider)(nil)
)

// Error is an API error from OpenAI. A caller reads the status code and
// the request ID from it with errors.As.
type Error = sdk.Error

// The text models that this package serves. Each one carries its output
// cap, so a call without [ai.MaxTokens] gets the full cap. For a model
// with no cap, the request omits the cap, and the API uses the cap of
// the model. The models are values, so a model from another package,
// such as openaichat, can use them too. The values are in the "openai"
// entry of pkg/catalog/models/models.json, and come from the model pages and the
// pricing page of the OpenAI docs. A prompt of more than 272000 input
// tokens has the prices of the tier.
var (
	GPT6Sol   = models.Must(providerID, "gpt-6-sol")
	GPT6Luna  = models.Must(providerID, "gpt-6-luna")
	GPT6Astra = models.Must(providerID, "gpt-6-astra")
	GPT5_6    = models.Must(providerID, "gpt-5.6")
)

// The image models that this package serves. [Provider.GenerateImage]
// takes them.
var (
	GPTImage2           = models.Must(providerID, "gpt-image-2")
	GPTImage2_5Flare    = models.Must(providerID, "gpt-image-2.5-flare")
	GPTImage2_5Sunburst = models.Must(providerID, "gpt-image-2.5-sunburst")
)

// providerID is the ID of this provider in pkg/catalog/models/models.json.
const providerID = "openai"

// Models returns the models that this package serves. The default text
// model comes first, so a caller that must pick one takes the first.
func Models() []ai.Model {
	return models.Of(providerID)
}

// PromptCacheKey sets the prompt cache key of a call. The API routes the
// calls with the same key to the same cache, so a conversation hits the
// cache more often. A caller uses one key per conversation. The
// openaichat provider reads the same option.
var PromptCacheKey = ai.NewOption[string]()

// Provider calls the OpenAI Responses API. Create one with [New].
type Provider struct {
	client sdk.Client
}

// Option configures [New].
type Option func(*config)

type config struct {
	apiKey     string
	apiKeySet  bool
	baseURL    string
	httpClient *http.Client
}

// APIKey sets the API key. If it is unset, the SDK reads
// OPENAI_API_KEY. An empty key replaces OPENAI_API_KEY too, so a
// provider for another server does not send the OpenAI key to it.
func APIKey(key string) Option {
	return func(c *config) {
		c.apiKey = key
		c.apiKeySet = true
	}
}

// BaseURL sets the base URL of the API, for a compatible API, a proxy,
// or a test server. If it is unset, the SDK reads OPENAI_BASE_URL. If
// that is unset too, the SDK uses https://api.openai.com/v1.
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
	if c.apiKeySet {
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
var OnPayload = ai.NewOption[func(params *responses.ResponseNewParams) error]()

// applyHook runs the [OnPayload] hook on params, if opts has one.
func applyHook(params *responses.ResponseNewParams, opts ai.Options) error {
	hook, ok := ai.GetOption(opts, OnPayload)
	if !ok || hook == nil {
		return nil
	}
	if err := hook(params); err != nil {
		return fmt.Errorf("openai: payload hook: %w", err)
	}
	return nil
}

// requestOptions returns the headers of [ai.Headers].
func requestOptions(opts ai.Options) []option.RequestOption {
	var reqOpts []option.RequestOption
	headers, _ := ai.GetOption(opts, ai.Headers)
	for name, value := range headers {
		reqOpts = append(reqOpts, option.WithHeader(name, value))
	}
	return reqOpts
}

// retryStatus reads the status and the headers of an API error.
func retryStatus(err error) (retry.Status, bool) {
	var apiErr *sdk.Error
	if !errors.As(err, &apiErr) || apiErr.Response == nil {
		return retry.Status{}, false
	}
	return retry.Status{
		Code:   apiErr.StatusCode,
		Header: apiErr.Response.Header,
	}, true
}

// openStream sends the request and returns the stream. It retries with
// the policy. The SDK sends the request before it returns the stream,
// so a retry happens before the first event.
func (p *Provider) openStream(
	ctx context.Context,
	params responses.ResponseNewParams,
	opts ai.Options,
) (*ssestream.Stream[responses.ResponseStreamEventUnion], error) {
	reqOpts := requestOptions(opts)
	send := func() (*ssestream.Stream[responses.ResponseStreamEventUnion], error) {
		stream := p.client.Responses.NewStreaming(ctx, params, reqOpts...)
		return stream, stream.Err()
	}

	stream, err := retry.Do(ctx, retry.PolicyOf(opts), send, retryStatus)
	if err != nil {
		return nil, fmt.Errorf("openai: %w", err)
	}
	return stream, nil
}

// StreamText streams one response. Each block pushes its start, delta,
// and end events with its index in the final message. The final message
// comes from the terminal event of the stream, so it holds the
// encrypted reasoning and the usage.
func (p *Provider) StreamText(
	ctx context.Context,
	model ai.Model,
	messages []ai.Message,
	opts ai.Options,
) *ai.EventStream {
	params, err := buildParams(model, messages, opts)
	if err != nil {
		return ai.ErrStream(err)
	}
	if err := applyHook(&params, opts); err != nil {
		return ai.ErrStream(err)
	}

	return ai.NewEventStream(func(push func(ai.Event)) (*ai.Message, error) {
		stream, err := p.openStream(ctx, params, opts)
		if err != nil {
			return nil, err
		}
		defer stream.Close()
		s := newStreamState(push)

		for stream.Next() {
			resp, done, err := s.handle(stream.Current())
			if err != nil {
				return nil, err
			}
			if done {
				msg, err := buildMessage(model, resp)
				if err != nil {
					return nil, err
				}
				s.closeOpen(msg)
				return msg, nil
			}
		}

		if err := stream.Err(); err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("openai: %w", err)
		}
		return nil, errors.New("openai: stream ended before the response completed")
	})
}

// blockKey names one block of the stream. An item with one block, such
// as reasoning or a function call, has part -1. A message has one block
// per content part.
type blockKey struct {
	output int64
	part   int64
}

// itemPart is the part of a block that is the whole item.
const itemPart = -1

// block is one open block of the stream.
type block struct {
	index int
	ended bool
	// summary is the index of the last reasoning summary. A new summary
	// starts with a blank line, as in the final message.
	summary int64
}

// streamState maps the events of the API to the events of a stream. The
// API sends the items in output order, so the blocks get the same index
// as in the message that [buildMessage] builds.
type streamState struct {
	push    func(ai.Event)
	started bool
	next    int
	blocks  map[blockKey]*block
}

func newStreamState(push func(ai.Event)) *streamState {
	return &streamState{
		push:   push,
		blocks: map[blockKey]*block{},
	}
}

// open returns the block for key. If the block is new, it gets the next
// index and pushes its start event. A server that skips the added
// events still gets a start event before the first delta.
func (s *streamState) open(key blockKey, kind ai.EventType) *block {
	if b, ok := s.blocks[key]; ok {
		return b
	}

	b := &block{
		index:   s.next,
		summary: -1,
	}
	s.blocks[key] = b
	s.next++
	s.push(ai.Event{Type: kind, ContentIndex: b.index})
	return b
}

// end pushes the end event of a block once.
func (s *streamState) end(b *block, ev ai.Event) {
	if b.ended {
		return
	}
	b.ended = true
	ev.ContentIndex = b.index
	s.push(ev)
}

// handle maps one event of the API. It returns the response and true on
// a terminal event.
func (s *streamState) handle(ev responses.ResponseStreamEventUnion) (responses.Response, bool, error) {
	if !s.started {
		s.started = true
		s.push(ai.Event{Type: ai.EventStart})
	}

	itemKey := blockKey{output: ev.OutputIndex, part: itemPart}
	partKey := blockKey{output: ev.OutputIndex, part: ev.ContentIndex}

	switch ev.Type {
	case "response.output_item.added":
		switch ev.Item.Type {
		case "reasoning":
			s.open(itemKey, ai.EventThinkStart)
		case "function_call":
			s.open(itemKey, ai.EventToolStart)
		}

	case "response.content_part.added":
		s.open(partKey, ai.EventTextStart)

	case "response.output_text.delta", "response.refusal.delta", "response.content_part.delta":
		b := s.open(partKey, ai.EventTextStart)
		s.push(ai.Event{Type: ai.EventTextDelta, ContentIndex: b.index, Delta: ev.Delta})

	case "response.reasoning_summary_text.delta":
		b := s.open(itemKey, ai.EventThinkStart)
		delta := ev.Delta
		if b.summary >= 0 && ev.SummaryIndex != b.summary {
			delta = summarySeparator + delta
		}
		b.summary = ev.SummaryIndex
		s.push(ai.Event{Type: ai.EventThinkDelta, ContentIndex: b.index, Delta: delta})

	case "response.reasoning_text.delta", "response.reasoning.delta":
		b := s.open(itemKey, ai.EventThinkStart)
		s.push(ai.Event{Type: ai.EventThinkDelta, ContentIndex: b.index, Delta: ev.Delta})

	case "response.function_call_arguments.delta":
		b := s.open(itemKey, ai.EventToolStart)
		s.push(ai.Event{Type: ai.EventToolDelta, ContentIndex: b.index, Delta: ev.Delta})

	case "response.output_item.done":
		s.itemDone(ev.OutputIndex, ev.Item)

	case "response.completed", "response.incomplete":
		return ev.Response, true, nil

	case "response.failed":
		apiErr := ev.Response.Error
		return responses.Response{}, false, fmt.Errorf("openai: response failed: %s: %s", apiErr.Code, apiErr.Message)

	case "error":
		return responses.Response{}, false, fmt.Errorf("openai: %s: %s", ev.Code, ev.Message)
	}

	return responses.Response{}, false, nil
}

// itemDone pushes the end events of a finished item.
func (s *streamState) itemDone(output int64, item responses.ResponseOutputItemUnion) {
	itemKey := blockKey{output: output, part: itemPart}

	switch item.Type {
	case "reasoning":
		b := s.open(itemKey, ai.EventThinkStart)
		s.end(b, ai.Event{Type: ai.EventThinkEnd})

	case "function_call":
		b := s.open(itemKey, ai.EventToolStart)
		call := toolCall(item)
		s.end(b, ai.Event{Type: ai.EventToolEnd, ToolCall: &call})

	case "message":
		for i, part := range item.Content {
			key := blockKey{output: output, part: int64(i)}
			b := s.open(key, ai.EventTextStart)
			s.end(b, ai.Event{Type: ai.EventTextEnd, Content: partText(part)})
		}
	}
}

// closeOpen pushes the end event of each block that is still open, in
// index order, from the final message. A server that skips the done
// events still gets an end event for each block.
func (s *streamState) closeOpen(msg *ai.Message) {
	open := make([]*block, 0, len(s.blocks))
	for _, b := range s.blocks {
		if !b.ended && b.index < len(msg.Content) {
			open = append(open, b)
		}
	}
	sort.Slice(open, func(i, j int) bool { return open[i].index < open[j].index })

	for _, b := range open {
		switch c := msg.Content[b.index].(type) {
		case ai.Text:
			s.end(b, ai.Event{Type: ai.EventTextEnd, Content: c.Text})
		case ai.ThinkingBlock:
			s.end(b, ai.Event{Type: ai.EventThinkEnd})
		case ai.ToolCall:
			s.end(b, ai.Event{Type: ai.EventToolEnd, ToolCall: &c})
		}
	}
}

// GenerateObject asks for a JSON object that matches schema, through
// structured outputs. The text of the response is the object.
func (p *Provider) GenerateObject(
	ctx context.Context,
	model ai.Model,
	messages []ai.Message,
	schema *jsonschema.Schema,
	opts ai.Options,
) (*ai.ObjectResponse, error) {
	params, err := buildObjectParams(model, messages, schema, opts)
	if err != nil {
		return nil, err
	}
	if err := applyHook(&params, opts); err != nil {
		return nil, err
	}

	reqOpts := requestOptions(opts)
	send := func() (*responses.Response, error) {
		return p.client.Responses.New(ctx, params, reqOpts...)
	}
	resp, err := retry.Do(ctx, retry.PolicyOf(opts), send, retryStatus)
	if err != nil {
		return nil, fmt.Errorf("openai: %w", err)
	}

	raw := resp.OutputText()
	if raw == "" {
		return nil, fmt.Errorf("openai: no object in response, status %q", resp.Status)
	}

	return &ai.ObjectResponse{
		Raw:   raw,
		Usage: mapUsage(resp.Usage),
	}, nil
}

// CountTokens counts the input tokens of a call. It sends the same
// input, tools, and reasoning config as a call.
func (p *Provider) CountTokens(
	ctx context.Context,
	model ai.Model,
	messages []ai.Message,
	opts ai.Options,
) (*ai.TokenCount, error) {
	params, err := buildCountTokensParams(model, messages, opts)
	if err != nil {
		return nil, err
	}

	resp, err := p.client.Responses.InputTokens.Count(ctx, params, requestOptions(opts)...)
	if err != nil {
		return nil, fmt.Errorf("openai: %w", err)
	}

	return &ai.TokenCount{Total: int(resp.InputTokens)}, nil
}

// summarySeparator joins the summaries of one reasoning item.
const summarySeparator = "\n\n"

// buildMessage builds the final message from the output of the
// response, in output order. A response that did not end, or ended for
// an unknown reason, is an error.
func buildMessage(model ai.Model, resp responses.Response) (*ai.Message, error) {
	var content []ai.Content
	var hasToolCall bool
	var refusal []string

	for _, item := range resp.Output {
		switch item.Type {
		case "reasoning":
			sig := signature{
				ID:               item.ID,
				EncryptedContent: item.EncryptedContent,
			}
			content = append(content, ai.ThinkingBlock{
				Thinking:  reasoningText(item),
				Signature: sig.String(),
			})

		case "message":
			for _, part := range item.Content {
				if part.Type == "refusal" {
					refusal = append(refusal, part.Refusal)
				}
				content = append(content, ai.Text{Text: partText(part)})
			}

		case "function_call":
			hasToolCall = true
			content = append(content, toolCall(item))
		}
	}

	stopReason, detail, err := mapStopReason(resp, hasToolCall, strings.Join(refusal, "\n"))
	if err != nil {
		return nil, err
	}

	usage := mapUsage(resp.Usage)
	usage.Cost = model.Pricing.Cost(usage)

	return &ai.Message{
		Role:       ai.RoleAssistant,
		Content:    content,
		Model:      model.ID,
		Usage:      usage,
		StopReason: stopReason,
		StopDetail: detail,
	}, nil
}

// reasoningText is the summaries of a reasoning item. A server that
// sends the raw reasoning and no summary gets the raw text.
func reasoningText(item responses.ResponseOutputItemUnion) string {
	var parts []string
	for _, s := range item.Summary {
		parts = append(parts, s.Text)
	}
	if len(parts) == 0 {
		for _, c := range item.Content {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, summarySeparator)
}

// partText is the text of a message part. A refusal part carries its
// text in another field.
func partText(part responses.ResponseOutputMessageContentUnion) string {
	if part.Type == "refusal" {
		return part.Refusal
	}
	return part.Text
}

// emptyInput is the input of a tool call without arguments.
const emptyInput = "{}"

// toolCall maps a function call item to a call. The ID is the call ID,
// because the tool result refers to it. The input is the JSON as the
// model wrote it. It can stop short at the output cap.
func toolCall(item responses.ResponseOutputItemUnion) ai.ToolCall {
	input := item.Arguments.OfString
	if input == "" {
		input = emptyInput
	}
	return ai.ToolCall{
		ID:    item.CallID,
		Name:  item.Name,
		Input: json.RawMessage(input),
	}
}

// The reasons of an incomplete response that the provider maps.
const (
	incompleteMaxTokens     = "max_output_tokens"
	incompleteContentFilter = "content_filter"
)

// mapStopReason derives the stop reason and its detail. The API has no
// stop reason. It has a status, the reason for an incomplete response,
// and the output. A content filter stop and a refusal part are a
// refusal. The detail of a refusal part is its text. Every other status
// or reason is an error, so a new one does not pass as a normal stop.
func mapStopReason(
	resp responses.Response,
	hasToolCall bool,
	refusal string,
) (ai.StopReason, string, error) {
	switch resp.Status {
	case responses.ResponseStatusCompleted:
		switch {
		case refusal != "":
			return ai.StopReasonRefusal, refusal, nil
		case hasToolCall:
			return ai.StopReasonToolUse, "", nil
		default:
			return ai.StopReasonStop, "", nil
		}
	case responses.ResponseStatusIncomplete:
		switch reason := resp.IncompleteDetails.Reason; reason {
		case incompleteMaxTokens:
			return ai.StopReasonLength, "", nil
		case incompleteContentFilter:
			return ai.StopReasonRefusal, incompleteContentFilter, nil
		default:
			return "", "", fmt.Errorf("openai: response incomplete: %q", reason)
		}
	default:
		return "", "", fmt.Errorf("openai: response ended with status %q", resp.Status)
	}
}

// mapUsage maps the usage of the API. The input tokens of the API hold
// the cached and the cache write tokens, so the provider subtracts them
// and the four fields do not overlap.
func mapUsage(u responses.ResponseUsage) ai.Usage {
	cacheRead := u.InputTokensDetails.CachedTokens
	cacheWrite := u.InputTokensDetails.CacheWriteTokens
	input := max(u.InputTokens-cacheRead-cacheWrite, 0)

	return ai.Usage{
		Input:      int(input),
		Output:     int(u.OutputTokens),
		CacheRead:  int(cacheRead),
		CacheWrite: int(cacheWrite),
	}
}
