package openrouter_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/internal/providertest"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/openrouter"
)

// decisionBody answers the question "is_bug" with 0.9.
const decisionBody = `{"id":"gen-1","model":"typesafe/jev-1.14","provider":"TypeSafe",` +
	`"answers":{"is_bug":{"type":"noul","noul":0.9}},` +
	`"usage":{"input_tokens":12,"output_tokens":0,"cost":0.0000012}}`

var jev = ai.Model{ID: "typesafe/jev-1.14"}

// jsonAnswer answers with a JSON body.
func jsonAnswer(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}
}

// completed answers with a short completed stream of the Responses API.
func completed(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprintf(w, "data: %s\n\n", `{"type":"response.completed","response":{"status":"completed",`+
		`"output":[{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"Hi"}]}]}}`)
}

func TestRequestPath(t *testing.T) {
	providertest.RunFake(t, providertest.Fake{
		New: func(baseURL string) ai.TextProvider {
			return openrouter.New(
				openrouter.APIKey("test-key"),
				openrouter.BaseURL(baseURL),
			)
		},
		Text:   completed,
		Decide: jsonAnswer(decisionBody),
	})
}

// recorder keeps the last request and answers with handler.
type recorder struct {
	handler http.HandlerFunc
	last    *http.Request
	body    []byte
}

func (r *recorder) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.last = req
	r.body, _ = io.ReadAll(req.Body)
	r.handler(w, req)
}

// newDecider returns a provider whose base URL ends in /api/v1, as the
// default does, and the recorder of its server.
func newDecider(t *testing.T, body string) (*openrouter.Provider, *recorder) {
	t.Helper()
	rec := &recorder{handler: jsonAnswer(body)}
	srv := httptest.NewServer(rec)
	t.Cleanup(srv.Close)
	p := openrouter.New(
		openrouter.APIKey("test-key"),
		openrouter.BaseURL(srv.URL+"/api/v1/"),
	)
	return p, rec
}

var (
	isBug   = ai.YesNo("is_bug", "Is it a defect?")
	isNew   = ai.YesNo("is_new", "Is it new?")
	team    = ai.Choice("team", "Which team?", ai.ChoiceOption{ID: "payments", Description: "Checkout."}, ai.ChoiceOption{ID: "frontend"})
	urgency = ai.Score("urgency", "How urgent?", ai.ScoreLevel{Label: "later"}, ai.ScoreLevel{Label: "now", Description: "It blocks revenue."})
	logo    = ai.Box("logo", "Find the logo.")
	ticket  = []ai.Message{ai.UserMessage("Pay is blank.")}
)

func TestDecide_Request(t *testing.T) {
	p, rec := newDecider(t, decisionBody)
	photo := ai.File{MediaType: "image/png", Data: []byte("png")}
	input := []ai.Message{
		ai.SystemMessage(ai.Text{Text: "Triage tickets."}),
		{Role: ai.RoleUser, Content: []ai.Content{ai.Text{Text: "Pay is blank."}, photo}},
		ai.AssistantMessage(
			ai.ThinkingBlock{Thinking: "Look at the logs."},
			ai.ToolCall{ID: "c1", Name: "logs", Input: json.RawMessage(`{"n":1}`)},
		),
		ai.ToolResultMessage("c1", ai.Text{Text: "HTTP 500"}),
		ai.AssistantMessage(ai.Text{Text: "The server fails."}),
	}
	questions := []ai.Question{
		isBug.YesWhen("Broken.").NoWhen("A question."),
		isNew,
		team,
		urgency,
		logo,
	}

	_, err := p.Decide(
		t.Context(),
		jev,
		input,
		questions,
		ai.Headers(map[string]string{"X-Team": "infra"}),
	)

	require.NoError(t, err)
	assert.Equal(t, http.MethodPost, rec.last.Method)
	assert.Equal(t, "/api/alpha/decisions", rec.last.URL.Path)
	assert.Equal(t, "Bearer test-key", rec.last.Header.Get("Authorization"))
	assert.Equal(t, "infra", rec.last.Header.Get("X-Team"))
	assert.JSONEq(t, `{
		"model": "typesafe/jev-1.14",
		"state": [
			{"role": "user", "content": [
				{"type": "text", "text": "Pay is blank."},
				{"type": "image_url", "image_url": {"url": "data:image/png;base64,cG5n"}}
			]},
			{"role": "system", "content": "Triage tickets."},
			{"role": "assistant", "tool_calls": [{"id": "c1", "name": "logs", "arguments": {"n": 1}}]},
			{"role": "tool", "tool_call_id": "c1", "content": "HTTP 500"},
			{"role": "assistant", "content": "The server fails."}
		],
		"questions": {
			"is_bug": {"type": "noul", "instructions": "Is it a defect?",
				"criteria": {"true": "Broken.", "false": "A question."}},
			"is_new": {"type": "noul", "instructions": "Is it new?"},
			"team": {"type": "choice", "instructions": "Which team?",
				"criteria": {"payments": "Checkout.", "frontend": "frontend"}},
			"urgency": {"type": "score", "instructions": "How urgent?",
				"criteria": ["later", "now: It blocks revenue."]},
			"logo": {"type": "box", "instructions": "Find the logo."}
		}
	}`, string(rec.body))
}

func TestDecide_Response(t *testing.T) {
	body := `{
		"answers": {"is_bug": {"type": "noul", "noul": 0.2}},
		"usage": {"input_tokens": 2000, "output_tokens": 63, "cost": 0.0002,
			"input_tokens_details": {"cached_tokens": 1500}}
	}`
	p, _ := newDecider(t, body)

	resp, err := p.Decide(t.Context(), jev, ticket, []ai.Question{isBug}, nil)

	require.NoError(t, err)
	assert.Equal(t, []ai.Answer{
		{
			Name:        "is_bug",
			Kind:        ai.KindYesNo,
			Probability: 0.2,
		},
	}, resp.Answers)
	assert.Equal(t, ai.Usage{
		Input:     500,
		Output:    63,
		CacheRead: 1500,
		Cost:      ai.Cost{Input: 0.0002},
	}, resp.Usage)
}

func TestDecide_Cost(t *testing.T) {
	tests := []struct {
		name  string
		usage string
		want  float64
	}{
		{
			name:  "the reported cost",
			usage: `{"input_tokens": 1000000, "cost": 0.02}`,
			want:  0.02,
		},
		{
			name:  "the prices of the model when no cost is reported",
			usage: `{"input_tokens": 1000000}`,
			want:  0.05,
		},
		{
			name: "the fee and the upstream cost of a key of the user",
			usage: `{"input_tokens": 1000000, "cost": 0.001, "is_byok": true,
				"cost_details": {"upstream_inference_cost": 0.03}}`,
			want: 0.031,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := `{"answers":{"is_bug":{"type":"noul","noul":0.9}},"usage":` + tt.usage + `}`
			p, _ := newDecider(t, body)
			priced := jev
			priced.Pricing = ai.Pricing{Input: 0.05}

			resp, err := p.Decide(t.Context(), priced, ticket, []ai.Question{isBug}, nil)

			require.NoError(t, err)
			assert.InDelta(t, tt.want, resp.Usage.Cost.Total(), 1e-12)
		})
	}
}

func TestDecide_ErrorInABody(t *testing.T) {
	body := `{"error":{"code":502,"message":"Upstream failed"}}`
	p, _ := newDecider(t, body)

	_, err := p.Decide(t.Context(), jev, ticket, []ai.Question{isBug}, nil)

	require.Error(t, err)
	assert.ErrorContains(t, err, "502 Upstream failed")
}
