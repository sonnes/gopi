package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"google.golang.org/genai/interactions/models/interactions"

	"github.com/sonnes/gopi/internal/decisionwire"
	"github.com/sonnes/gopi/internal/httpapi"
	"github.com/sonnes/gopi/internal/transcript"
	"github.com/sonnes/gopi/pkg/ai"
)

var _ ai.DecisionProvider = (*Provider)(nil)

// decisionsRoute is the route of the Decisions API, after the base URL
// and the API version.
const decisionsRoute = "/decisions"

// keyHeader is the header that carries the API key.
const keyHeader = "x-goog-api-key"

type decisionRequest struct {
	Model     string                           `json:"model"`
	Input     []interactions.Content           `json:"input"`
	Questions map[string]decisionwire.Question `json:"questions"`
}

// noulCriteria describes the two outcomes of a noul question.
type noulCriteria struct {
	True  string `json:"true_description"`
	False string `json:"false_description"`
}

type decisionResponse struct {
	Answers map[string]decisionwire.Answer `json:"answers"`
	Usage   decisionUsage                  `json:"usage"`
}

type decisionUsage struct {
	TotalInputTokens  int `json:"total_input_tokens"`
	TotalOutputTokens int `json:"total_output_tokens"`
}

// Decide answers the questions about input with the Decisions API of
// Gemini, such as with the model gemini-decision-1. The Go SDK has no
// Decisions API, so the provider posts to /decisions under the base URL
// and the API version of the SDK, with the key and the HTTP client of
// the SDK.
//
// The API reads text, images, audio, video, and PDF documents. It
// takes no tool calls, tool results, or thoughts. So a tool call and a
// tool result go as text that names the tool, and a thinking block is
// dropped. The API has no box question. A box question or a file of
// another type returns an error that wraps [errors.ErrUnsupported],
// before the request.
//
// A failed answer is an [*Error]. A safety block is an [*Error] with
// the status 400 and the message of the API. The cost comes from the
// prices of model.
func (p *Provider) Decide(
	ctx context.Context,
	model ai.Model,
	input []ai.Message,
	questions []ai.Question,
	opts ai.Options,
) (*ai.DecisionResponse, error) {
	if p.err != nil {
		return nil, p.err
	}
	if err := checkDecision(input, questions); err != nil {
		return nil, err
	}
	body, err := json.Marshal(decisionRequest{
		Model:     model.ID,
		Input:     decisionInput(input),
		Questions: wireQuestions(questions),
	})
	if err != nil {
		return nil, fmt.Errorf("gemini: decisions: %w", err)
	}

	cfg := p.client.ClientConfig()
	url := strings.TrimSuffix(cfg.HTTPOptions.BaseURL, "/") +
		"/" + cfg.HTTPOptions.APIVersion + decisionsRoute
	header := http.Header{}
	headers, _ := ai.GetOption(opts, ai.Headers)
	for name, value := range headers {
		header.Set(name, value)
	}
	header.Set(keyHeader, cfg.APIKey)

	send := func() ([]byte, error) {
		raw, err := httpapi.Post(ctx, cfg.HTTPClient, url, "", body, header)
		return raw, statusError(err)
	}
	raw, err := call(ctx, opts, send)
	if err != nil {
		return nil, err
	}

	var resp decisionResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("gemini: decisions: %w", err)
	}
	return resp.toAI(model, questions), nil
}

// checkDecision rejects a box question and a file that the API does not
// read.
func checkDecision(input []ai.Message, questions []ai.Question) error {
	for _, q := range questions {
		if q.Kind == ai.KindBox {
			return fmt.Errorf(
				"gemini: decisions have no box question %q: %w",
				q.Name,
				errors.ErrUnsupported,
			)
		}
	}
	for _, msg := range input {
		for _, c := range msg.Content {
			f, ok := c.(ai.File)
			if ok && !decisionFile(f.MediaType) {
				return fmt.Errorf(
					"gemini: decisions do not take a file of type %s: %w",
					f.MediaType,
					errors.ErrUnsupported,
				)
			}
		}
	}
	return nil
}

// decisionFile reports whether the API reads a file of the media type.
func decisionFile(mediaType string) bool {
	return kindOf(mediaType) != fileUnknown
}

// decisionInput maps the messages to content items, in the order of
// [transcript.Order]. Each system message goes as a system-reminder, as
// in [convertMessages]. The API joins adjacent text items with no
// separator, so each text item ends with a newline.
func decisionInput(messages []ai.Message) []interactions.Content {
	ordered := transcript.Order(messages, false)
	names := toolNames(ordered)

	var out []interactions.Content
	for _, msg := range ordered {
		switch msg.Role {
		case ai.RoleUser, ai.RoleAssistant:
			out = append(out, blockItems(msg.Content)...)
		case ai.RoleToolResult:
			out = append(out, toolResultItems(msg, names[msg.ToolCallID])...)
		}
	}
	return out
}

// blockItems maps text, files, and tool calls. It drops thinking
// blocks, because a decision model reads no reasoning.
func blockItems(blocks []ai.Content) []interactions.Content {
	var out []interactions.Content
	for _, c := range blocks {
		switch v := c.(type) {
		case ai.Text:
			if v.Text != "" {
				out = append(out, textItem(v.Text))
			}
		case ai.File:
			out = append(out, fileContent(v))
		case ai.ToolCall:
			text := "[tool call " + v.Name + "] " + toolArguments(v.Input)
			out = append(out, textItem(text))
		}
	}
	return out
}

// toolResultItems maps a tool result to one text item that names the
// tool, then its files.
func toolResultItems(msg ai.Message, name string) []interactions.Content {
	label := "[tool result " + name + "]"
	if msg.IsError {
		label = "[tool error " + name + "]"
	}
	var text []string
	var files []interactions.Content
	for _, c := range msg.Content {
		switch v := c.(type) {
		case ai.Text:
			if v.Text != "" {
				text = append(text, v.Text)
			}
		case ai.File:
			files = append(files, fileContent(v))
		}
	}
	if len(text) > 0 {
		label += " " + strings.Join(text, "\n")
	}
	return append([]interactions.Content{textItem(label)}, files...)
}

func textItem(text string) interactions.Content {
	return interactions.NewContent(textContent(text + "\n"))
}

// toolArguments returns the input of a tool call as compact JSON, or an
// empty object if the input is not JSON.
func toolArguments(input json.RawMessage) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, input); err != nil {
		return "{}"
	}
	return buf.String()
}

func wireQuestions(questions []ai.Question) map[string]decisionwire.Question {
	out := make(map[string]decisionwire.Question, len(questions))
	for _, q := range questions {
		wire := decisionwire.Question{Instructions: q.Instructions}
		switch q.Kind {
		case ai.KindYesNo:
			wire.Type = decisionwire.TypeNoul
			if q.Yes != "" || q.No != "" {
				wire.Criteria = noulCriteria{
					True:  q.Yes,
					False: q.No,
				}
			}
		case ai.KindChoice:
			wire.Type = decisionwire.TypeChoice
			// An option with no description sends an empty description.
			// The API accepts it.
			criteria := make(map[string]string, len(q.Options))
			for _, o := range q.Options {
				criteria[o.ID] = o.Description
			}
			wire.Criteria = criteria
		case ai.KindScore:
			wire.Type = decisionwire.TypeScore
			wire.Criteria = decisionwire.LevelTexts(q.Levels)
		}
		out[q.Name] = wire
	}
	return out
}

// toAI maps the response with [decisionwire.Answers].
func (r decisionResponse) toAI(model ai.Model, questions []ai.Question) *ai.DecisionResponse {
	usage := ai.Usage{
		Input:  r.Usage.TotalInputTokens,
		Output: r.Usage.TotalOutputTokens,
	}
	usage.Cost = model.Pricing.Cost(usage)
	return &ai.DecisionResponse{
		Answers: decisionwire.Answers(r.Answers, questions),
		Usage:   usage,
	}
}
