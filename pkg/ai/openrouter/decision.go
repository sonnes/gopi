package openrouter

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/sonnes/gopi/internal/decisionwire"
	"github.com/sonnes/gopi/internal/httpapi"
	"github.com/sonnes/gopi/internal/retry"
	"github.com/sonnes/gopi/internal/transcript"
	"github.com/sonnes/gopi/pkg/ai"
)

var _ ai.DecisionProvider = (*Provider)(nil)

// The parts of the URL of the Decisions API. The API is in alpha, so
// its route is not under the base URL /api/v1.
const (
	versionSuffix  = "/v1"
	decisionsRoute = "/alpha/decisions"
)

// decisionsURL returns the URL of the Decisions API: the base URL with
// its version replaced.
func (p *Provider) decisionsURL() string {
	return strings.TrimSuffix(p.cfg.baseURL, versionSuffix) + decisionsRoute
}

type decisionRequest struct {
	Model     string                           `json:"model"`
	State     []stateMessage                   `json:"state"`
	Questions map[string]decisionwire.Question `json:"questions"`
}

// stateMessage is one message of the state. The state is free JSON, so
// the provider keeps the shape of a chat message.
type stateMessage struct {
	Role       string          `json:"role"`
	Content    any             `json:"content,omitempty"`
	ToolCalls  []stateToolCall `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

type stateToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// statePart is one part of the content of a message with an image.
type statePart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

type imageURL struct {
	URL string `json:"url"`
}

type decisionResponse struct {
	Answers map[string]decisionwire.Answer `json:"answers"`
	Usage   decisionUsage                  `json:"usage"`
	// Error is the error of a failed call that has the status 200.
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type decisionUsage struct {
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	Cost         float64 `json:"cost"`
	// IsBYOK reports that the call used a key of the user. Then Cost is
	// the fee of OpenRouter, and the upstream cost is apart.
	IsBYOK      bool `json:"is_byok"`
	CostDetails struct {
		UpstreamInferenceCost float64 `json:"upstream_inference_cost"`
	} `json:"cost_details"`
	InputTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"input_tokens_details"`
}

// Decide answers the questions about input with the Decisions API of
// OpenRouter. The API serves each decision model of OpenRouter, such as
// typesafe/jev-1.14 and openai/gpt-6-luna-decisions. A box question
// works only on a model that takes it. The API rejects it on the others
// with HTTP 400, and the error does not wrap [errors.ErrUnsupported],
// because the provider cannot tell that 400 from another.
//
// The cost is the cost that OpenRouter reports. With a key of the user,
// it is the fee plus the upstream cost. If OpenRouter reports no cost,
// the cost comes from the prices of model.
func (p *Provider) Decide(
	ctx context.Context,
	model ai.Model,
	input []ai.Message,
	questions []ai.Question,
	opts ai.Options,
) (*ai.DecisionResponse, error) {
	if mediaType, ok := transcript.NotImage(input); ok {
		return nil, fmt.Errorf(
			"openrouter: decisions do not take a file of type %s: %w",
			mediaType,
			errors.ErrUnsupported,
		)
	}
	body, err := json.Marshal(decisionRequest{
		Model:     model.ID,
		State:     buildState(input),
		Questions: wireQuestions(questions),
	})
	if err != nil {
		return nil, fmt.Errorf("openrouter: decisions: %w", err)
	}

	header := http.Header{}
	headers, _ := ai.GetOption(opts, ai.Headers)
	for name, value := range headers {
		header.Set(name, value)
	}
	send := func() ([]byte, error) {
		return httpapi.Post(ctx, p.cfg.httpClient, p.decisionsURL(), p.cfg.apiKey, body, header)
	}
	raw, err := retry.Call(ctx, opts, "openrouter: decisions", statusOf, send)
	if err != nil {
		return nil, err
	}

	var resp decisionResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("openrouter: decisions: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("openrouter: decisions: %d %s", resp.Error.Code, resp.Error.Message)
	}
	return resp.toAI(model, questions), nil
}

// statusOf reads the status and the headers of a failed request.
func statusOf(err error) (retry.Status, bool) {
	var status *httpapi.StatusError
	if !errors.As(err, &status) {
		return retry.Status{}, false
	}
	return retry.Status{
		Code:   status.Code,
		Header: status.Header,
	}, true
}

// buildState maps the messages to the state, in the order of
// [transcript.Order]. It drops thinking blocks, because a decision
// model reads no reasoning.
func buildState(messages []ai.Message) []stateMessage {
	var out []stateMessage
	for _, msg := range transcript.Order(messages, true) {
		switch msg.Role {
		case ai.RoleSystem:
			if text := transcript.SystemText(msg.Content); text != "" {
				out = append(out, stateMessage{Role: "system", Content: text})
			}
		case ai.RoleUser:
			if content := stateContent(msg.Content); content != nil {
				out = append(out, stateMessage{Role: "user", Content: content})
			}
		case ai.RoleAssistant:
			out = append(out, assistantState(msg.Content)...)
		case ai.RoleToolResult:
			out = append(out, stateMessage{
				Role:       "tool",
				ToolCallID: msg.ToolCallID,
				Content:    stateContent(msg.Content),
			})
		}
	}
	return out
}

// assistantState maps the text of an assistant message to one message,
// and its tool calls to another.
func assistantState(content []ai.Content) []stateMessage {
	var calls []stateToolCall
	var text []string
	for _, c := range content {
		switch v := c.(type) {
		case ai.Text:
			if v.Text != "" {
				text = append(text, v.Text)
			}
		case ai.ToolCall:
			calls = append(calls, stateToolCall{
				ID:        v.ID,
				Name:      v.Name,
				Arguments: toolArguments(v.Input),
			})
		}
	}
	var out []stateMessage
	if len(text) > 0 {
		out = append(out, stateMessage{Role: "assistant", Content: strings.Join(text, "\n")})
	}
	if len(calls) > 0 {
		out = append(out, stateMessage{Role: "assistant", ToolCalls: calls})
	}
	return out
}

// toolArguments returns the input of a tool call, or an empty object if
// the input is not JSON.
func toolArguments(input json.RawMessage) json.RawMessage {
	if !json.Valid(input) {
		return json.RawMessage(`{}`)
	}
	return input
}

// stateContent maps blocks to one string, or to a list of parts if the
// blocks hold an image. It returns nil if the blocks hold nothing.
func stateContent(content []ai.Content) any {
	var parts []statePart
	var text []string
	hasImage := false
	for _, c := range content {
		switch v := c.(type) {
		case ai.Text:
			if v.Text == "" {
				continue
			}
			text = append(text, v.Text)
			parts = append(parts, statePart{Type: "text", Text: v.Text})
		case ai.File:
			hasImage = true
			url := "data:" + v.MediaType + ";base64," + base64.StdEncoding.EncodeToString(v.Data)
			parts = append(parts, statePart{Type: "image_url", ImageURL: &imageURL{URL: url}})
		}
	}
	switch {
	case hasImage:
		return parts
	case len(text) > 0:
		return strings.Join(text, "\n")
	default:
		return nil
	}
}

func wireQuestions(questions []ai.Question) map[string]decisionwire.Question {
	out := make(map[string]decisionwire.Question, len(questions))
	for _, q := range questions {
		wire := decisionwire.Question{Instructions: q.Instructions}
		switch q.Kind {
		case ai.KindYesNo:
			wire.Type = decisionwire.TypeNoul
			if q.Yes != "" || q.No != "" {
				wire.Criteria = map[string]string{
					"true":  q.Yes,
					"false": q.No,
				}
			}
		case ai.KindChoice:
			wire.Type = decisionwire.TypeChoice
			criteria := make(map[string]string, len(q.Options))
			for _, o := range q.Options {
				criteria[o.ID] = cmp.Or(o.Description, o.ID)
			}
			wire.Criteria = criteria
		case ai.KindScore:
			wire.Type = decisionwire.TypeScore
			wire.Criteria = decisionwire.LevelTexts(q.Levels)
		case ai.KindBox:
			wire.Type = decisionwire.TypeBox
		}
		out[q.Name] = wire
	}
	return out
}

// toAI maps the response with [decisionwire.Answers]. A noul answer has
// no confidence.
func (r decisionResponse) toAI(model ai.Model, questions []ai.Question) *ai.DecisionResponse {
	return &ai.DecisionResponse{
		Answers: decisionwire.Answers(r.Answers, questions),
		Usage:   r.Usage.toAI(model),
	}
}

// toAI maps the usage. The input tokens hold the cached tokens, so the
// provider subtracts them and the fields do not overlap.
func (u decisionUsage) toAI(model ai.Model) ai.Usage {
	cached := u.InputTokensDetails.CachedTokens
	usage := ai.Usage{
		Input:     max(u.InputTokens-cached, 0),
		Output:    u.OutputTokens,
		CacheRead: cached,
	}
	cost := u.Cost
	if u.IsBYOK {
		cost += u.CostDetails.UpstreamInferenceCost
	}
	if cost > 0 {
		usage.Cost = ai.Cost{Input: cost}
		return usage
	}
	usage.Cost = model.Pricing.Cost(usage)
	return usage
}
