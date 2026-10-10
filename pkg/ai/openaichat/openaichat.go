// Package openaichat is the provider for the OpenAI Chat Completions
// API and for the servers that copy it, such as OpenRouter and
// Fireworks. It implements [ai.TextProvider], [ai.ObjectProvider], and
// [ai.ModelLister].
//
// A caller must obey these rules:
//
//   - The package has no models. A caller uses a model from
//     [github.com/sonnes/gopi/pkg/ai/openai], or its own [ai.Model] for
//     another server. A model for another server sets MaxTokens,
//     because a server such as Fireworks has a low default cap.
//   - A request holds only the fields that every server accepts.
//     [CacheBreakpoints] adds the OpenAI cache fields, which Fireworks
//     rejects.
//   - A thinking block goes back to the server as reasoning_content. A
//     model with interleaved thinking needs it between tool calls.
//   - A model that takes no system message in the conversation gets
//     each system message as a system-reminder in a user message.
//   - An API error wraps [Error]. A caller reads the status code with
//     errors.As.
//
// The reasons are in docs/concepts/ai/openai.md.
package openaichat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/packages/ssestream"

	"github.com/sonnes/gopi/internal/retry"
	"github.com/sonnes/gopi/pkg/ai"
)

// The capabilities of the provider, checked at compile time.
var (
	_ ai.TextProvider   = (*Provider)(nil)
	_ ai.ObjectProvider = (*Provider)(nil)
)

// Error is an API error from the server. A caller reads the status code
// and the request ID from it with errors.As.
type Error = sdk.Error

// Provider calls a Chat Completions API. Create one with [New].
type Provider struct {
	client sdk.Client
	cfg    config
}

// Option configures [New].
type Option func(*config)

type config struct {
	apiKey           string
	baseURL          string
	httpClient       *http.Client
	cacheBreakpoints bool
}

// APIKey sets the API key. If it is unset, the SDK reads
// OPENAI_API_KEY.
func APIKey(key string) Option {
	return func(c *config) { c.apiKey = key }
}

// BaseURL sets the base URL of the API, for another server, a proxy, or
// a test server. For example, https://openrouter.ai/api/v1 or
// https://api.fireworks.ai/inference/v1. If it is unset, the SDK reads
// OPENAI_BASE_URL. If that is unset too, the SDK uses
// https://api.openai.com/v1.
func BaseURL(url string) Option {
	return func(c *config) { c.baseURL = url }
}

// HTTPClient sets the HTTP client. A test passes a client that records
// and replays traffic. If it is unset, the SDK uses its own client.
func HTTPClient(client *http.Client) Option {
	return func(c *config) { c.httpClient = client }
}

// CacheBreakpoints marks the end of the system prompt as a prompt cache
// breakpoint. OpenAI reads the mark on a GPT-5.6 or later model, and
// OpenRouter turns it into the cache mark of the model. Fireworks
// rejects the field, so the mark is off by default.
func CacheBreakpoints() Option {
	return func(c *config) { c.cacheBreakpoints = true }
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

	return &Provider{
		client: sdk.NewClient(clientOpts...),
		cfg:    c,
	}
}

// OnPayload sets a hook that reads or changes the request of a stream
// or object call before the provider sends it. An error from the hook
// stops the call. The hook runs once for each call, before the first
// attempt.
var OnPayload = ai.NewOption[func(params *sdk.ChatCompletionNewParams) error]()

// applyHook runs the [OnPayload] hook on params, if opts has one.
func applyHook(params *sdk.ChatCompletionNewParams, opts ai.Options) error {
	hook, ok := ai.GetOption(opts, OnPayload)
	if !ok || hook == nil {
		return nil
	}
	if err := hook(params); err != nil {
		return fmt.Errorf("openaichat: payload hook: %w", err)
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
	params sdk.ChatCompletionNewParams,
	opts ai.Options,
) (*ssestream.Stream[sdk.ChatCompletionChunk], error) {
	reqOpts := requestOptions(opts)
	send := func() (*ssestream.Stream[sdk.ChatCompletionChunk], error) {
		stream := p.client.Chat.Completions.NewStreaming(ctx, params, reqOpts...)
		return stream, stream.Err()
	}

	stream, err := retry.Do(ctx, retry.PolicyOf(opts), send, retryStatus)
	if err != nil {
		return nil, fmt.Errorf("openaichat: %w", err)
	}
	return stream, nil
}

// StreamText streams one response. A new kind of delta closes the open
// block and opens the next one, so the blocks keep the order of the
// stream. The request asks for usage, because OpenAI sends none in a
// stream without it.
func (p *Provider) StreamText(
	ctx context.Context,
	model ai.Model,
	messages []ai.Message,
	opts ai.Options,
) *ai.EventStream {
	params, err := buildParams(p.cfg, model, messages, opts)
	if err != nil {
		return ai.ErrStream(err)
	}
	params.StreamOptions.IncludeUsage = param.NewOpt(true)
	if err := applyHook(&params, opts); err != nil {
		return ai.ErrStream(err)
	}

	return ai.NewEventStream(func(push func(ai.Event)) (*ai.Message, error) {
		stream, err := p.openStream(ctx, params, opts)
		if err != nil {
			return nil, err
		}
		defer stream.Close()
		s := &streamState{
			push:  push,
			tools: map[int64]*block{},
		}

		for stream.Next() {
			if err := s.handle(stream.Current()); err != nil {
				return nil, err
			}
		}

		if err := stream.Err(); err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("openaichat: %w", err)
		}

		s.closeCurrent()
		return s.message(model)
	})
}

// block is one block of the stream, in the order of the final message.
type block struct {
	kind  ai.EventType // the start event of the block
	index int
	text  strings.Builder
	call  ai.ToolCall
	ended bool
}

// streamState folds the chunks into blocks and pushes their events.
type streamState struct {
	push    func(ai.Event)
	started bool
	blocks  []*block
	current *block
	// tools maps the index of a tool call in the stream to its block.
	tools   map[int64]*block
	finish  string
	refusal strings.Builder
	usage   ai.Usage
}

// handle folds one chunk. A chunk with an error field fails the stream.
// OpenRouter sends one when the model fails after the stream starts.
func (s *streamState) handle(chunk sdk.ChatCompletionChunk) error {
	if !s.started {
		s.started = true
		s.push(ai.Event{Type: ai.EventStart})
	}

	if field, ok := chunk.JSON.ExtraFields["error"]; ok && field.Raw() != "null" {
		return fmt.Errorf("openaichat: %s", errorMessage(field.Raw()))
	}

	// OpenRouter repeats the finish reason on the usage chunk, and
	// Fireworks can send usage on the finish chunk, so usage comes from
	// any chunk that has it.
	if chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 {
		s.usage = mapUsage(chunk.Usage)
	}

	for _, choice := range chunk.Choices {
		s.handleDelta(choice.Delta)
		if choice.FinishReason == "error" {
			return errors.New("openaichat: the model stopped with an error")
		}
		if choice.FinishReason != "" {
			s.finish = choice.FinishReason
		}
	}

	return nil
}

// handleDelta folds the delta of one choice.
func (s *streamState) handleDelta(delta sdk.ChatCompletionChunkChoiceDelta) {
	if reasoning := reasoningDelta(delta); reasoning != "" {
		b := s.continueBlock(ai.EventThinkStart)
		b.text.WriteString(reasoning)
		s.push(ai.Event{Type: ai.EventThinkDelta, ContentIndex: b.index, Delta: reasoning})
	}

	text := delta.Content
	if delta.Refusal != "" {
		s.refusal.WriteString(delta.Refusal)
		text += delta.Refusal
	}
	if text != "" {
		b := s.continueBlock(ai.EventTextStart)
		b.text.WriteString(text)
		s.push(ai.Event{Type: ai.EventTextDelta, ContentIndex: b.index, Delta: text})
	}

	for _, tc := range delta.ToolCalls {
		b, ok := s.tools[tc.Index]
		if !ok {
			b = s.open(ai.EventToolStart)
			s.tools[tc.Index] = b
		}
		if b.call.ID == "" {
			b.call.ID = tc.ID
		}
		if b.call.Name == "" {
			b.call.Name = tc.Function.Name
		}

		args := tc.Function.Arguments
		if args != "" {
			b.text.WriteString(args)
			s.push(ai.Event{Type: ai.EventToolDelta, ContentIndex: b.index, Delta: args})
		}
	}
}

// continueBlock returns the open block if it is of kind, else it opens a
// new block of kind.
func (s *streamState) continueBlock(kind ai.EventType) *block {
	if s.current != nil && s.current.kind == kind {
		return s.current
	}
	return s.open(kind)
}

// open closes the open block and opens a block of kind.
func (s *streamState) open(kind ai.EventType) *block {
	s.closeCurrent()

	b := &block{
		kind:  kind,
		index: len(s.blocks),
	}
	s.blocks = append(s.blocks, b)
	s.current = b
	s.push(ai.Event{Type: kind, ContentIndex: b.index})
	return b
}

// closeCurrent pushes the end event of the open block.
func (s *streamState) closeCurrent() {
	b := s.current
	if b == nil || b.ended {
		return
	}
	b.ended = true

	ev := ai.Event{ContentIndex: b.index}
	switch b.kind {
	case ai.EventTextStart:
		ev.Type = ai.EventTextEnd
		ev.Content = b.text.String()
	case ai.EventThinkStart:
		ev.Type = ai.EventThinkEnd
	case ai.EventToolStart:
		call := toolCall(b)
		ev.Type = ai.EventToolEnd
		ev.ToolCall = &call
	}
	s.push(ev)
}

// message builds the final message from the blocks, in stream order. A
// stream with no finish reason, or an unknown one, is an error.
func (s *streamState) message(model ai.Model) (*ai.Message, error) {
	content := make([]ai.Content, 0, len(s.blocks))
	hasToolCall := false

	for _, b := range s.blocks {
		switch b.kind {
		case ai.EventTextStart:
			content = append(content, ai.Text{Text: b.text.String()})
		case ai.EventThinkStart:
			content = append(content, ai.ThinkingBlock{Thinking: b.text.String()})
		case ai.EventToolStart:
			hasToolCall = true
			content = append(content, toolCall(b))
		}
	}

	stopReason, detail, err := mapStopReason(s.finish, hasToolCall, s.refusal.String())
	if err != nil {
		return nil, err
	}

	usage := s.usage
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

// emptyInput is the input of a tool call without arguments.
const emptyInput = "{}"

// toolCall is the call of a tool block. The input is the JSON as the
// model wrote it. It can stop short at the output cap.
func toolCall(b *block) ai.ToolCall {
	call := b.call
	input := b.text.String()
	if input == "" {
		input = emptyInput
	}
	call.Input = json.RawMessage(input)
	return call
}

// reasoningFields are the fields of a delta that carry reasoning.
// Fireworks sends reasoning_content, and OpenRouter sends reasoning.
var reasoningFields = []string{reasoningField, "reasoning"}

// reasoningDelta is the reasoning text of a delta. The SDK does not know
// the fields, so they come from the extra fields of the delta.
func reasoningDelta(delta sdk.ChatCompletionChunkChoiceDelta) string {
	for _, key := range reasoningFields {
		field, ok := delta.JSON.ExtraFields[key]
		if !ok {
			continue
		}

		var text string
		if err := json.Unmarshal([]byte(field.Raw()), &text); err == nil && text != "" {
			return text
		}
	}
	return ""
}

// errorMessage is the message of an error field, or the raw field if it
// has no message.
func errorMessage(raw string) string {
	var body struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(raw), &body); err == nil && body.Message != "" {
		return body.Message
	}
	return raw
}

// The finish reasons that the provider maps.
const (
	finishStop          = "stop"
	finishLength        = "length"
	finishToolCalls     = "tool_calls"
	finishFunctionCall  = "function_call"
	finishContentFilter = "content_filter"
)

// mapStopReason maps the finish reason and returns the stop detail. A
// server can send stop with tool calls, so a tool call is a tool use
// stop. A content filter stop and a refusal are a refusal. The detail of
// a refusal is its text. A stream with no finish reason ended early, and
// an unknown reason must not pass as a normal stop, so both are errors.
func mapStopReason(finish string, hasToolCall bool, refusal string) (ai.StopReason, string, error) {
	switch finish {
	case finishLength:
		return ai.StopReasonLength, "", nil
	case finishContentFilter:
		return ai.StopReasonRefusal, finishContentFilter, nil
	case finishStop, finishToolCalls, finishFunctionCall:
		switch {
		case refusal != "":
			return ai.StopReasonRefusal, refusal, nil
		case hasToolCall:
			return ai.StopReasonToolUse, "", nil
		default:
			return ai.StopReasonStop, "", nil
		}
	case "":
		return "", "", errors.New("openaichat: the stream ended with no finish reason")
	default:
		return "", "", fmt.Errorf("openaichat: unknown finish reason %q", finish)
	}
}

// mapUsage maps the usage of the API. The prompt tokens hold the cached
// and the cache write tokens, so the provider subtracts them and the
// four fields do not overlap.
func mapUsage(u sdk.CompletionUsage) ai.Usage {
	cacheRead := u.PromptTokensDetails.CachedTokens
	cacheWrite := u.PromptTokensDetails.CacheWriteTokens
	input := max(u.PromptTokens-cacheRead-cacheWrite, 0)

	return ai.Usage{
		Input:      int(input),
		Output:     int(u.CompletionTokens),
		CacheRead:  int(cacheRead),
		CacheWrite: int(cacheWrite),
	}
}

// GenerateObject asks for a JSON object that matches schema, through
// structured outputs. The content of the response is the object.
func (p *Provider) GenerateObject(
	ctx context.Context,
	model ai.Model,
	messages []ai.Message,
	schema *jsonschema.Schema,
	opts ai.Options,
) (*ai.ObjectResponse, error) {
	params, err := buildObjectParams(p.cfg, model, messages, schema, opts)
	if err != nil {
		return nil, err
	}
	if err := applyHook(&params, opts); err != nil {
		return nil, err
	}

	reqOpts := requestOptions(opts)
	send := func() (*sdk.ChatCompletion, error) {
		return p.client.Chat.Completions.New(ctx, params, reqOpts...)
	}
	resp, err := retry.Do(ctx, retry.PolicyOf(opts), send, retryStatus)
	if err != nil {
		return nil, fmt.Errorf("openaichat: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, errors.New("openaichat: no choice in response")
	}

	choice := resp.Choices[0]
	if choice.Message.Refusal != "" {
		return nil, fmt.Errorf("openaichat: the model refused: %s", choice.Message.Refusal)
	}
	if choice.Message.Content == "" {
		return nil, fmt.Errorf("openaichat: no object in response, finish reason %q", choice.FinishReason)
	}

	return &ai.ObjectResponse{
		Raw:   choice.Message.Content,
		Usage: mapUsage(resp.Usage),
	}, nil
}
