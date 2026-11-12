package openai

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/openai/openai-go/v3/responses"

	"github.com/sonnes/gopi/internal/transcript"
	"github.com/sonnes/gopi/pkg/ai"
)

var _ ai.DecisionProvider = (*Provider)(nil)

// decisionsPath is the path of the Decisions API, under the base URL.
const decisionsPath = "decisions"

// The answer types of the Decisions API.
const (
	answerPredicate = "predicate"
	answerChoice    = "choice"
	answerScore     = "score"
	answerRefusal   = "refusal"
)

// decisionRequest is the body of POST /decisions. openai-go has no type
// for it.
type decisionRequest struct {
	Model          string                       `json:"model"`
	Input          responses.ResponseInputParam `json:"input"`
	Questions      []decisionQuestion           `json:"questions"`
	PromptCacheKey string                       `json:"prompt_cache_key,omitempty"`
}

type decisionQuestion struct {
	Type         string           `json:"type"`
	Name         string           `json:"name"`
	Instructions string           `json:"instructions"`
	Choices      []decisionChoice `json:"choices,omitempty"`
	Levels       []decisionLevel  `json:"levels,omitempty"`
}

type decisionChoice struct {
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
}

type decisionLevel struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// decisionResponse is the body of the response of POST /decisions.
type decisionResponse struct {
	Answers []decisionAnswer        `json:"answers"`
	Usage   responses.ResponseUsage `json:"usage"`
}

type decisionAnswer struct {
	Type          string                `json:"type"`
	Name          string                `json:"name"`
	Probability   float64               `json:"probability"`
	Choice        string                `json:"choice"`
	Score         float64               `json:"score"`
	Confidence    float64               `json:"confidence"`
	Probabilities []decisionProbability `json:"probabilities"`
}

// decisionProbability is the probability of one choice or one level.
// Value is the ID of a choice, or the index of a level. A level also
// has its label.
type decisionProbability struct {
	Value       any     `json:"value"`
	Label       string  `json:"label"`
	Probability float64 `json:"probability"`
}

// Decide answers the questions about input with the Decisions API. The
// API takes no box question, so Decide returns an error that wraps
// [errors.ErrUnsupported] for one, and sends nothing. The provider adds
// the outcome text of a yes-or-no question to its instructions, because
// the API has no field for it. If the call has [PromptCacheKey], the
// request sends it, unless the retention is [ai.CacheNone].
func (p *Provider) Decide(
	ctx context.Context,
	model ai.Model,
	input []ai.Message,
	questions []ai.Question,
	opts ai.Options,
) (*ai.DecisionResponse, error) {
	body, err := buildDecisionRequest(model, input, questions, opts)
	if err != nil {
		return nil, err
	}

	reqOpts := requestOptions(opts)
	send := func() (decisionResponse, error) {
		var out decisionResponse
		err := p.client.Post(ctx, decisionsPath, body, &out, reqOpts...)
		return out, err
	}
	resp, err := call(ctx, opts, send)
	if err != nil {
		return nil, err
	}
	return resp.toAI(model, questions), nil
}

func buildDecisionRequest(
	model ai.Model,
	input []ai.Message,
	questions []ai.Question,
	opts ai.Options,
) (decisionRequest, error) {
	if mediaType, ok := transcript.NotImage(input); ok {
		return decisionRequest{}, unsupported("a file of type " + mediaType)
	}

	cfg := convertConfig{systemMessages: true}
	wireInput, err := convertMessages(withoutThinking(input), cfg)
	if err != nil {
		return decisionRequest{}, err
	}

	wireQuestions := make([]decisionQuestion, 0, len(questions))
	for _, q := range questions {
		wire, err := wireQuestion(q)
		if err != nil {
			return decisionRequest{}, err
		}
		wireQuestions = append(wireQuestions, wire)
	}

	out := decisionRequest{
		Model:     model.ID,
		Input:     wireInput,
		Questions: wireQuestions,
	}
	if key, ok := promptCacheKey(opts); ok {
		out.PromptCacheKey = key
	}
	return out, nil
}

func wireQuestion(q ai.Question) (decisionQuestion, error) {
	out := decisionQuestion{
		Name:         q.Name,
		Instructions: q.Instructions,
	}
	switch q.Kind {
	case ai.KindYesNo:
		out.Type = answerPredicate
		out.Instructions = withOutcomes(q)
	case ai.KindChoice:
		out.Type = answerChoice
		for _, o := range q.Options {
			out.Choices = append(out.Choices, decisionChoice{
				Value:       o.ID,
				Description: o.Description,
			})
		}
	case ai.KindScore:
		out.Type = answerScore
		for _, level := range q.Levels {
			out.Levels = append(out.Levels, decisionLevel{
				Label:       level.Label,
				Description: level.Description,
			})
		}
	default:
		return decisionQuestion{}, unsupported(fmt.Sprintf("question %q of kind %s", q.Name, q.Kind))
	}
	return out, nil
}

// withOutcomes returns the instructions of a yes-or-no question, with a
// line for each outcome that it describes.
func withOutcomes(q ai.Question) string {
	var b strings.Builder
	b.WriteString(q.Instructions)
	if q.Yes != "" {
		b.WriteString("\nYes: ")
		b.WriteString(q.Yes)
	}
	if q.No != "" {
		b.WriteString("\nNo: ")
		b.WriteString(q.No)
	}
	return b.String()
}

func unsupported(what string) error {
	return fmt.Errorf("openai: decisions do not take %s: %w", what, errors.ErrUnsupported)
}

// withoutThinking returns the messages with no thinking blocks. The
// Decisions API takes no reasoning items.
func withoutThinking(messages []ai.Message) []ai.Message {
	out := make([]ai.Message, 0, len(messages))
	for _, msg := range messages {
		content := make([]ai.Content, 0, len(msg.Content))
		for _, c := range msg.Content {
			if _, ok := c.(ai.ThinkingBlock); !ok {
				content = append(content, c)
			}
		}
		msg.Content = content
		out = append(out, msg)
	}
	return out
}

// toAI maps the response. The kind of an answer comes from its type,
// and a refusal has no kind. A predicate answer has no confidence.
func (r decisionResponse) toAI(model ai.Model, questions []ai.Question) *ai.DecisionResponse {
	levels := make(map[string][]ai.ScoreLevel, len(questions))
	for _, q := range questions {
		levels[q.Name] = q.Levels
	}

	answers := make([]ai.Answer, 0, len(r.Answers))
	for _, a := range r.Answers {
		out := ai.Answer{Name: a.Name}
		switch a.Type {
		case answerPredicate:
			out.Kind = ai.KindYesNo
			out.Probability = a.Probability
		case answerChoice:
			out.Kind = ai.KindChoice
			out.Choice = a.Choice
			out.Confidence = a.Confidence
			out.ChoiceProbabilities = choiceProbabilities(a.Probabilities)
		case answerScore:
			out.Kind = ai.KindScore
			out.Score = a.Score
			out.Confidence = a.Confidence
			out.LevelProbabilities = levelProbabilities(a.Probabilities, levels[a.Name])
		case answerRefusal:
			out.Refused = true
		default:
			out.Kind = ai.QuestionKind(a.Type)
		}
		answers = append(answers, out)
	}

	usage := mapUsage(r.Usage)
	usage.Cost = model.Pricing.Cost(usage)
	return &ai.DecisionResponse{
		Answers: answers,
		Usage:   usage,
	}
}

// choiceProbabilities returns the probability of each option by its ID,
// or nil if the API reports none.
func choiceProbabilities(ps []decisionProbability) map[string]float64 {
	if len(ps) == 0 {
		return nil
	}
	out := make(map[string]float64, len(ps))
	for _, p := range ps {
		out[fmt.Sprint(p.Value)] = p.Probability
	}
	return out
}

// levelProbabilities puts the probability of each level at the index of
// its label. A probability with an unknown label goes at the index of
// its value. It returns nil if the API reports none.
func levelProbabilities(ps []decisionProbability, levels []ai.ScoreLevel) []float64 {
	if len(ps) == 0 {
		return nil
	}
	out := make([]float64, len(levels))
	for _, p := range ps {
		index := slices.IndexFunc(levels, func(l ai.ScoreLevel) bool {
			return l.Label == p.Label
		})
		if index < 0 {
			index = valueIndex(p.Value)
		}
		if index >= 0 && index < len(out) {
			out[index] = p.Probability
		}
	}
	return out
}

// valueIndex returns the index in the value of a level probability, or
// -1. JSON decodes a number as a float64.
func valueIndex(value any) int {
	n, ok := value.(float64)
	if !ok || n != math.Trunc(n) {
		return -1
	}
	return int(n)
}
