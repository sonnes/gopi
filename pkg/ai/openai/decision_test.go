package openai_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/openai"
)

// decisionBody answers the question "is_bug" with 0.9.
const decisionBody = `{"answers":[{"type":"predicate","name":"is_bug","probability":0.9}],` +
	`"usage":{"input_tokens":12,"output_tokens":0}}`

var decider = ai.Model{
	ID:      "gpt-6-luna",
	Pricing: ai.Pricing{Input: 0.1, CacheRead: 0.01},
}

var ticket = []ai.Message{ai.UserMessage("Pay shows a blank page.")}

var (
	isBug   = ai.YesNo("is_bug", "Is it a defect?")
	isNew   = ai.YesNo("is_new", "Is it new?")
	team    = ai.Choice("team", "Which team?", ai.ChoiceOption{ID: "payments", Description: "Checkout."}, ai.ChoiceOption{ID: "frontend"})
	urgency = ai.Score("urgency", "How urgent?", ai.ScoreLevel{Label: "later"}, ai.ScoreLevel{Label: "now", Description: "It blocks revenue."})
)

func TestDecide_Request(t *testing.T) {
	s := &sequence{handlers: []http.HandlerFunc{answer(decisionBody)}}
	p := sequenceProvider(t, s)
	input := []ai.Message{
		ai.UserMessage("Pay shows a blank page."),
		ai.AssistantMessage(
			ai.ThinkingBlock{Thinking: "Look at the logs."},
			ai.ToolCall{ID: "c1", Name: "logs", Input: json.RawMessage(`{}`)},
		),
		ai.ToolResultMessage("c1", ai.Text{Text: "HTTP 500"}),
	}
	questions := []ai.Question{
		isBug.YesWhen("Broken.").NoWhen("A question."),
		isNew,
		team,
		urgency,
	}

	_, err := p.Decide(
		t.Context(),
		decider,
		input,
		questions,
		ai.JoinOptions(
			openai.PromptCacheKey("ticket-1"),
			ai.Headers(map[string]string{"X-Team": "infra"}),
		),
	)

	require.NoError(t, err)
	assert.Equal(t, http.MethodPost, s.last.Method)
	assert.Equal(t, "/decisions", s.last.URL.Path)
	assert.Equal(t, "infra", s.last.Header.Get("X-Team"))
	assert.JSONEq(t, `{
		"model": "gpt-6-luna",
		"prompt_cache_key": "ticket-1",
		"input": [
			{"role": "user", "content": [{"type": "input_text", "text": "Pay shows a blank page."}]},
			{"type": "function_call", "call_id": "c1", "name": "logs", "arguments": "{}"},
			{"type": "function_call_output", "call_id": "c1", "output": "HTTP 500"}
		],
		"questions": [
			{"type": "predicate", "name": "is_bug", "instructions": "Is it a defect?\nYes: Broken.\nNo: A question."},
			{"type": "predicate", "name": "is_new", "instructions": "Is it new?"},
			{"type": "choice", "name": "team", "instructions": "Which team?", "choices": [
				{"value": "payments", "description": "Checkout."},
				{"value": "frontend"}
			]},
			{"type": "score", "name": "urgency", "instructions": "How urgent?", "levels": [
				{"label": "later"},
				{"label": "now", "description": "It blocks revenue."}
			]}
		]
	}`, string(s.body))
}

func TestDecide_Response(t *testing.T) {
	body := `{
		"answers": [
			{"type": "predicate", "name": "is_bug", "probability": 0.2},
			{"type": "choice", "name": "team", "choice": "payments", "confidence": 0.8,
				"probabilities": [{"value": "payments", "probability": 0.8}, {"value": "frontend", "probability": 0.2}]},
			{"type": "score", "name": "urgency", "score": 0.7, "confidence": 0.7,
				"probabilities": [{"value": 1, "label": "now", "probability": 0.7}, {"value": 0, "label": "later", "probability": 0.3}]},
			{"type": "refusal", "name": "is_new"}
		],
		"usage": {"input_tokens": 2000, "output_tokens": 0,
			"input_tokens_details": {"cached_tokens": 1200, "cache_write_tokens": 300}}
	}`
	s := &sequence{handlers: []http.HandlerFunc{answer(body)}}
	p := sequenceProvider(t, s)
	questions := []ai.Question{isBug, team, urgency, isNew}

	resp, err := p.Decide(t.Context(), decider, ticket, questions, nil)

	require.NoError(t, err)
	assert.Equal(t, []ai.Answer{
		{
			Name:        "is_bug",
			Kind:        ai.KindYesNo,
			Probability: 0.2,
		},
		{
			Name:       "team",
			Kind:       ai.KindChoice,
			Choice:     "payments",
			Confidence: 0.8,
			ChoiceProbabilities: map[string]float64{
				"payments": 0.8,
				"frontend": 0.2,
			},
		},
		{
			Name:               "urgency",
			Kind:               ai.KindScore,
			Score:              0.7,
			Confidence:         0.7,
			LevelProbabilities: []float64{0.3, 0.7},
		},
		{
			Name:    "is_new",
			Refused: true,
		},
	}, resp.Answers)
	assert.Equal(t, ai.Usage{
		Input:      500,
		CacheRead:  1200,
		CacheWrite: 300,
		Cost: ai.Cost{
			Input:     0.00005,
			CacheRead: 0.000012,
		},
	}, resp.Usage)
}

func TestDecide_NoProbabilities(t *testing.T) {
	body := `{"answers": [
		{"type": "choice", "name": "team", "choice": "payments", "confidence": 0.8},
		{"type": "score", "name": "urgency", "score": 1, "confidence": 0.9}
	]}`
	s := &sequence{handlers: []http.HandlerFunc{answer(body)}}
	p := sequenceProvider(t, s)

	resp, err := p.Decide(t.Context(), decider, ticket, []ai.Question{team, urgency}, nil)

	require.NoError(t, err)
	assert.Nil(t, resp.Answers[0].ChoiceProbabilities)
	assert.Nil(t, resp.Answers[1].LevelProbabilities)
}

func TestDecide_BoxQuestion(t *testing.T) {
	s := &sequence{handlers: []http.HandlerFunc{answer(decisionBody)}}
	p := sequenceProvider(t, s)
	input := []ai.Message{{
		Role:    ai.RoleUser,
		Content: []ai.Content{ai.File{MediaType: "image/png", Data: []byte("data")}},
	}}

	_, err := p.Decide(t.Context(), decider, input, []ai.Question{ai.Box("logo", "Find the logo.")}, nil)

	require.ErrorIs(t, err, errors.ErrUnsupported)
	assert.Zero(t, s.calls.Load(), "the provider sends no request")
}
