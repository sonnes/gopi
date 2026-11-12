package gemini_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/internal/transcript"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/gemini"
)

// decisionBody answers the question "is_bug" with 0.9.
const decisionBody = `{
	"answers": {"is_bug": {"type": "noul", "noul": 0.9, "confidence": 0.9}},
	"usage": {"total_input_tokens": 12, "total_output_tokens": 0, "total_tokens": 12}
}`

var decisionModel = ai.Model{
	ID:      "gemini-decision-1",
	Pricing: ai.Pricing{Input: 0.1},
}

// decisionRecorder keeps the last request and answers with handler.
type decisionRecorder struct {
	handler http.HandlerFunc
	calls   int
	last    *http.Request
	body    []byte
}

func (r *decisionRecorder) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.calls++
	r.last = req
	r.body, _ = io.ReadAll(req.Body)
	r.handler(w, req)
}

// newDecider returns a provider for a server that answers each request
// with body, and the recorder of the server.
func newDecider(t *testing.T, body string) (*gemini.Provider, *decisionRecorder) {
	t.Helper()
	rec := &decisionRecorder{handler: answer(body)}
	srv := httptest.NewServer(rec)
	t.Cleanup(srv.Close)
	p := gemini.New(
		gemini.APIKey("test-key"),
		gemini.BaseURL(srv.URL),
	)
	return p, rec
}

var (
	isBug   = ai.YesNo("is_bug", "Is it a defect?")
	isNew   = ai.YesNo("is_new", "Is it new?")
	team    = ai.Choice("team", "Which team?", ai.ChoiceOption{ID: "payments", Description: "Checkout."}, ai.ChoiceOption{ID: "frontend"})
	urgency = ai.Score("urgency", "How urgent?", ai.ScoreLevel{Label: "later"}, ai.ScoreLevel{Label: "now", Description: "It blocks revenue."})
	ticket  = []ai.Message{ai.UserMessage("Pay is blank.")}
)

func TestDecide_Request(t *testing.T) {
	p, rec := newDecider(t, decisionBody)
	questions := []ai.Question{
		isBug.YesWhen("Broken.").NoWhen("A question."),
		isNew,
		team,
		urgency,
	}

	_, err := p.Decide(
		t.Context(),
		decisionModel,
		ticket,
		questions,
		ai.Headers(map[string]string{"X-Team": "infra"}),
	)

	require.NoError(t, err)
	assert.Equal(t, http.MethodPost, rec.last.Method)
	assert.Equal(t, "/v1beta/decisions", rec.last.URL.Path)
	assert.Equal(t, "test-key", rec.last.Header.Get("x-goog-api-key"))
	assert.Empty(t, rec.last.Header.Get("Authorization"))
	assert.Equal(t, "infra", rec.last.Header.Get("X-Team"))
	assert.JSONEq(t, `{
		"model": "gemini-decision-1",
		"input": [{"type": "text", "text": "Pay is blank.\n"}],
		"questions": {
			"is_bug": {"type": "noul", "instructions": "Is it a defect?",
				"criteria": {"true_description": "Broken.", "false_description": "A question."}},
			"is_new": {"type": "noul", "instructions": "Is it new?"},
			"team": {"type": "choice", "instructions": "Which team?",
				"criteria": {"payments": "Checkout.", "frontend": ""}},
			"urgency": {"type": "score", "instructions": "How urgent?",
				"criteria": ["later", "now: It blocks revenue."]}
		}
	}`, string(rec.body))
}

func TestDecide_Input(t *testing.T) {
	p, rec := newDecider(t, decisionBody)
	pdf := ai.File{MediaType: "application/pdf", Data: []byte("pdf")}
	png := ai.File{MediaType: "image/png", Data: []byte("png")}
	wav := ai.File{MediaType: "audio/wav", Data: []byte("wav")}
	mp4 := ai.File{MediaType: "video/mp4", Data: []byte("mp4")}
	input := []ai.Message{
		ai.SystemMessage(ai.Text{Text: "Triage tickets."}),
		{Role: ai.RoleUser, Content: []ai.Content{ai.Text{Text: "Read the spec."}, pdf, wav, mp4}},
		ai.AssistantMessage(
			ai.ThinkingBlock{Thinking: "Look at the file.", Signature: `{"gemini":"sig-1"}`},
			ai.Text{Text: "I will read it."},
			ai.ToolCall{ID: "c1", Name: "read_file", Input: json.RawMessage(`{"path": "a.go"}`)},
		),
		{Role: ai.RoleToolResult, ToolCallID: "c1", Content: []ai.Content{ai.Text{Text: "package a"}, png}},
		ai.AssistantMessage(ai.Text{Text: "It is package a."}),
	}

	_, err := p.Decide(t.Context(), decisionModel, input, []ai.Question{isBug}, nil)

	require.NoError(t, err)
	reminder := transcript.Reminder("Triage tickets.")
	want, err := json.Marshal([]map[string]string{
		{"type": "text", "text": reminder + "\n"},
		{"type": "text", "text": "Read the spec.\n"},
		{"type": "document", "mime_type": "application/pdf", "data": "cGRm"},
		{"type": "audio", "mime_type": "audio/wav", "data": "d2F2"},
		{"type": "video", "mime_type": "video/mp4", "data": "bXA0"},
		{"type": "text", "text": "I will read it.\n"},
		{"type": "text", "text": `[tool call read_file] {"path":"a.go"}` + "\n"},
		{"type": "text", "text": "[tool result read_file] package a\n"},
		{"type": "image", "mime_type": "image/png", "data": "cG5n"},
		{"type": "text", "text": "It is package a.\n"},
	})
	require.NoError(t, err)
	assert.JSONEq(t, string(want), inputOf(t, rec.body))
}

func TestDecide_ToolError(t *testing.T) {
	p, rec := newDecider(t, decisionBody)
	input := []ai.Message{
		ai.AssistantMessage(ai.ToolCall{ID: "c1", Name: "bash", Input: json.RawMessage(`not json`)}),
		{Role: ai.RoleToolResult, ToolCallID: "c1", IsError: true, Content: []ai.Content{ai.Text{Text: "exit 1"}}},
	}

	_, err := p.Decide(t.Context(), decisionModel, input, []ai.Question{isBug}, nil)

	require.NoError(t, err)
	assert.JSONEq(t, `[
		{"type": "text", "text": "[tool call bash] {}\n"},
		{"type": "text", "text": "[tool error bash] exit 1\n"}
	]`, inputOf(t, rec.body))
}

func inputOf(t *testing.T, body []byte) string {
	t.Helper()
	var got struct {
		Input json.RawMessage `json:"input"`
	}
	require.NoError(t, json.Unmarshal(body, &got))
	return string(got.Input)
}

func TestDecide_BoxQuestion(t *testing.T) {
	p, rec := newDecider(t, decisionBody)
	photo := ai.File{MediaType: "image/png", Data: []byte("png")}
	input := []ai.Message{{Role: ai.RoleUser, Content: []ai.Content{photo}}}

	_, err := p.Decide(t.Context(), decisionModel, input, []ai.Question{ai.Box("logo", "Find the logo.")}, nil)

	require.ErrorIs(t, err, errors.ErrUnsupported)
	assert.Zero(t, rec.calls, "the provider sends nothing")
}

func TestDecide_Response(t *testing.T) {
	body := `{
		"answers": {"is_bug": {"type": "noul", "noul": 0.8, "confidence": 0.8}},
		"usage": {"total_input_tokens": 1000000, "total_output_tokens": 3, "total_tokens": 1000003}
	}`
	p, _ := newDecider(t, body)

	resp, err := p.Decide(t.Context(), decisionModel, ticket, []ai.Question{isBug}, nil)

	require.NoError(t, err)
	assert.Equal(t, []ai.Answer{
		{
			Name:        "is_bug",
			Kind:        ai.KindYesNo,
			Probability: 0.8,
			Confidence:  0.8,
		},
	}, resp.Answers)
	assert.Equal(t, 1000000, resp.Usage.Input)
	assert.Equal(t, 3, resp.Usage.Output)
	assert.InDelta(t, 0.1, resp.Usage.Cost.Total(), 1e-9)
}

func TestDecide_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error": {"code": "safety", "message": "The input was blocked."}}`)
	}))
	t.Cleanup(srv.Close)
	p := gemini.New(gemini.APIKey("test-key"), gemini.BaseURL(srv.URL))

	_, err := p.Decide(t.Context(), decisionModel, ticket, []ai.Question{isBug}, nil)

	var apiErr *gemini.Error
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
	assert.Equal(t, "The input was blocked.", apiErr.Message)
}
