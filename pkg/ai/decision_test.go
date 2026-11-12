package ai_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
)

// decider records the call that it gets and returns resp.
type decider struct {
	calls     int
	model     ai.Model
	input     []ai.Message
	questions []ai.Question
	opts      ai.Options
	resp      *ai.DecisionResponse
}

func (d *decider) Decide(
	_ context.Context,
	model ai.Model,
	input []ai.Message,
	questions []ai.Question,
	opts ai.Options,
) (*ai.DecisionResponse, error) {
	d.calls++
	d.model = model
	d.input = input
	d.questions = questions
	d.opts = opts
	return d.resp, nil
}

var ticket = []ai.Message{ai.UserMessage("Pay shows a blank page.")}

var photo = []ai.Message{{
	Role:    ai.RoleUser,
	Content: []ai.Content{ai.File{MediaType: "image/png", Data: []byte("png")}},
}}

var (
	payments = ai.ChoiceOption{ID: "payments", Description: "Checkout or billing."}
	frontend = ai.ChoiceOption{ID: "frontend"}
	later    = ai.ScoreLevel{Label: "later"}
	now      = ai.ScoreLevel{Label: "now", Description: "It blocks revenue."}
)

func TestQuestion_OutcomesReturnACopy(t *testing.T) {
	base := ai.YesNo("is_bug", "Is it a defect?")

	got := base.YesWhen("Broken.").NoWhen("A question.")

	assert.Equal(t, "Broken.", got.Yes)
	assert.Equal(t, "A question.", got.No)
	assert.Empty(t, base.Yes, "the receiver keeps no outcome")
	assert.Empty(t, base.No)
}

func TestQuestionsOf(t *testing.T) {
	isBug := ai.YesNo("is_bug", "Is it a defect?")
	team := ai.Choice("team", "Which team?", payments, frontend)

	opts := ai.JoinOptions(isBug, ai.MaxRetries(2), ai.JoinOptions(team))

	assert.Equal(t, []ai.Question{isBug, team}, ai.QuestionsOf(opts))
	retries, ok := ai.GetOption(opts, ai.MaxRetries)
	require.True(t, ok)
	assert.Equal(t, 2, retries)
	assert.Equal(t, []ai.Question{isBug}, ai.QuestionsOf(isBug))
	assert.Empty(t, ai.QuestionsOf(nil))
}

func TestDecide_Invalid(t *testing.T) {
	longName := strings.Repeat("a", 65)
	longID := strings.Repeat("a", 129)

	tests := []struct {
		name    string
		input   []ai.Message
		opts    []ai.Options
		wantErr string
	}{
		{
			name:    "no input",
			opts:    []ai.Options{ai.YesNo("is_bug", "Is it a defect?")},
			wantErr: "no input",
		},
		{
			name:    "no questions",
			input:   ticket,
			opts:    []ai.Options{ai.MaxRetries(1)},
			wantErr: "no questions",
		},
		{
			name:    "a name with a space",
			input:   ticket,
			opts:    []ai.Options{ai.YesNo("is bug", "Is it a defect?")},
			wantErr: `"is bug"`,
		},
		{
			name:    "a long name",
			input:   ticket,
			opts:    []ai.Options{ai.YesNo(longName, "Is it a defect?")},
			wantErr: "1 to 64 characters",
		},
		{
			name:  "a name twice",
			input: ticket,
			opts: []ai.Options{
				ai.YesNo("is_bug", "Is it a defect?"),
				ai.YesNo("is_bug", "Is it broken?"),
			},
			wantErr: "twice",
		},
		{
			name:    "no instructions",
			input:   ticket,
			opts:    []ai.Options{ai.YesNo("is_bug", "")},
			wantErr: "no instructions",
		},
		{
			name:    "an unknown kind",
			input:   ticket,
			opts:    []ai.Options{ai.Question{Name: "x", Kind: "guess", Instructions: "Guess."}},
			wantErr: "unknown kind",
		},
		{
			name:    "a choice with one option",
			input:   ticket,
			opts:    []ai.Options{ai.Choice("team", "Which team?", payments)},
			wantErr: "2 options",
		},
		{
			name:    "an empty option ID",
			input:   ticket,
			opts:    []ai.Options{ai.Choice("team", "Which team?", payments, ai.ChoiceOption{})},
			wantErr: "1 to 128 characters",
		},
		{
			name:    "a long option ID",
			input:   ticket,
			opts:    []ai.Options{ai.Choice("team", "Which team?", payments, ai.ChoiceOption{ID: longID})},
			wantErr: "1 to 128 characters",
		},
		{
			name:    "an option twice",
			input:   ticket,
			opts:    []ai.Options{ai.Choice("team", "Which team?", payments, payments)},
			wantErr: `option "payments" twice`,
		},
		{
			name:    "a score with one level",
			input:   ticket,
			opts:    []ai.Options{ai.Score("urgency", "How urgent?", now)},
			wantErr: "2 levels",
		},
		{
			name:    "a level with no label",
			input:   ticket,
			opts:    []ai.Options{ai.Score("urgency", "How urgent?", later, ai.ScoreLevel{Description: "Soon."})},
			wantErr: "no label",
		},
		{
			name:    "a level twice",
			input:   ticket,
			opts:    []ai.Options{ai.Score("urgency", "How urgent?", later, later)},
			wantErr: `level "later" twice`,
		},
		{
			name:    "outcomes on a choice",
			input:   ticket,
			opts:    []ai.Options{ai.Choice("team", "Which team?", payments, frontend).YesWhen("Broken.")},
			wantErr: "has outcomes",
		},
		{
			name:  "options on a yes-or-no question",
			input: ticket,
			opts: []ai.Options{ai.Question{
				Name:         "is_bug",
				Kind:         ai.KindYesNo,
				Instructions: "Is it a defect?",
				Options:      []ai.ChoiceOption{payments},
			}},
			wantErr: "has options",
		},
		{
			name:  "levels on a box question",
			input: photo,
			opts: []ai.Options{ai.Question{
				Name:         "logo",
				Kind:         ai.KindBox,
				Instructions: "Find the logo.",
				Levels:       []ai.ScoreLevel{later, now},
			}},
			wantErr: "has levels",
		},
		{
			name:    "a box with no image",
			input:   ticket,
			opts:    []ai.Options{ai.Box("logo", "Find the logo.")},
			wantErr: "needs an image",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &decider{}
			dm := ai.DecisionModel{Provider: d}

			_, err := dm.Decide(t.Context(), tt.input, tt.opts...)

			require.ErrorIs(t, err, ai.ErrInvalidDecision)
			assert.ErrorContains(t, err, tt.wantErr)
			assert.Zero(t, d.calls, "the provider gets no invalid request")
		})
	}
}

func TestDecide(t *testing.T) {
	isBug := ai.YesNo("is_bug", "Is it a defect?")
	team := ai.Choice("team", "Which team?", payments, frontend)
	logo := ai.Box("logo", "Find the logo.")
	d := &decider{resp: &ai.DecisionResponse{
		// The provider answers in another order, the refusal has no kind,
		// and the yes-or-no answer has no confidence.
		Answers: []ai.Answer{
			{Name: "logo", Refused: true, Confidence: 0.5, Bounds: &ai.Bounds{Right: 1}},
			{Name: "team", Kind: ai.KindChoice, Choice: "payments", Confidence: 0.8},
			{Name: "is_bug", Kind: ai.KindYesNo, Probability: 0.2},
		},
		Usage: ai.Usage{Input: 12},
	}}
	dm := ai.DecisionModel{Model: ai.Model{ID: "jev"}, Provider: d}
	input := append(ticket, photo...)

	resp, err := dm.Decide(t.Context(), input, isBug, ai.MaxRetries(2), team, logo)

	require.NoError(t, err)
	assert.Equal(t, "jev", d.model.ID)
	assert.Equal(t, input, d.input)
	assert.Equal(t, []ai.Question{isBug, team, logo}, d.questions)
	retries, _ := ai.GetOption(d.opts, ai.MaxRetries)
	assert.Equal(t, 2, retries)
	assert.Equal(t, []ai.Answer{
		{Name: "is_bug", Kind: ai.KindYesNo, Probability: 0.2, Confidence: 0.8},
		{Name: "team", Kind: ai.KindChoice, Choice: "payments", Confidence: 0.8},
		{Name: "logo", Kind: ai.KindBox, Refused: true},
	}, resp.Answers)
	assert.Equal(t, 12, resp.Usage.Input)
}

func TestDecide_KeepsAReportedConfidence(t *testing.T) {
	d := &decider{resp: &ai.DecisionResponse{Answers: []ai.Answer{
		{Name: "is_bug", Kind: ai.KindYesNo, Probability: 0.9, Confidence: 0.6},
	}}}
	dm := ai.DecisionModel{Provider: d}

	resp, err := dm.Decide(t.Context(), ticket, ai.YesNo("is_bug", "Is it a defect?"))

	require.NoError(t, err)
	assert.InDelta(t, 0.6, resp.Answers[0].Confidence, 1e-9)
}

func TestDecide_BadAnswers(t *testing.T) {
	isBug := ai.YesNo("is_bug", "Is it a defect?")
	team := ai.Choice("team", "Which team?", payments, frontend)
	logo := ai.Box("logo", "Find the logo.")

	tests := []struct {
		name    string
		answers []ai.Answer
		wantErr string
	}{
		{
			name:    "a missing answer",
			answers: []ai.Answer{{Name: "is_bug", Kind: ai.KindYesNo}},
			wantErr: `no answer to the question "team"`,
		},
		{
			name: "an answer of another kind",
			answers: []ai.Answer{
				{Name: "is_bug", Kind: ai.KindYesNo},
				{Name: "team", Kind: ai.KindScore},
			},
			wantErr: "is a score answer",
		},
		{
			name: "a box with no bounds",
			answers: []ai.Answer{
				{Name: "is_bug", Kind: ai.KindYesNo},
				{Name: "team", Kind: ai.KindChoice},
				{Name: "logo", Kind: ai.KindBox},
			},
			wantErr: "no bounds",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &decider{resp: &ai.DecisionResponse{Answers: tt.answers}}
			dm := ai.DecisionModel{Provider: d}

			_, err := dm.Decide(t.Context(), photo, isBug, team, logo)

			require.ErrorIs(t, err, ai.ErrNoAnswer)
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestDecisionResponse_Accessors(t *testing.T) {
	bounds := ai.Bounds{Top: 0.1, Bottom: 0.2, Left: 0.6, Right: 0.9}
	resp := &ai.DecisionResponse{Answers: []ai.Answer{
		{Name: "is_bug", Kind: ai.KindYesNo, Probability: 0.9},
		{Name: "team", Kind: ai.KindChoice, Choice: "payments"},
		{Name: "urgency", Kind: ai.KindScore, Score: 1.5},
		{Name: "logo", Kind: ai.KindBox, Bounds: &bounds},
		{Name: "is_new", Kind: ai.KindYesNo, Refused: true},
	}}

	p, err := resp.YesNo("is_bug")
	require.NoError(t, err)
	assert.InDelta(t, 0.9, p, 1e-9)

	choice, err := resp.Choice("team")
	require.NoError(t, err)
	assert.Equal(t, "payments", choice)

	score, err := resp.Score("urgency")
	require.NoError(t, err)
	assert.InDelta(t, 1.5, score, 1e-9)

	box, err := resp.Box("logo")
	require.NoError(t, err)
	assert.Equal(t, bounds, box)

	empty := &ai.DecisionResponse{Answers: []ai.Answer{{Name: "logo", Kind: ai.KindBox}}}
	_, err = empty.Box("logo")
	require.ErrorIs(t, err, ai.ErrNoAnswer)

	_, err = resp.YesNo("is_new")
	require.ErrorIs(t, err, ai.ErrRefused)
	assert.ErrorContains(t, err, `"is_new"`)

	_, err = resp.YesNo("is_bgu")
	require.ErrorIs(t, err, ai.ErrNoAnswer)
	assert.ErrorContains(t, err, `"is_bgu"`)

	_, err = resp.Choice("is_bug")
	require.ErrorIs(t, err, ai.ErrNoAnswer)
	assert.ErrorContains(t, err, "not a choice answer")

	a, ok := resp.Answer("team")
	require.True(t, ok)
	assert.Equal(t, "payments", a.Choice)
	_, ok = resp.Answer("none")
	assert.False(t, ok)
}

func TestAnswer_Yes(t *testing.T) {
	assert.True(t, ai.Answer{Probability: 0.5}.Yes())
	assert.False(t, ai.Answer{Probability: 0.49}.Yes())
}

func TestAnswer_JSON(t *testing.T) {
	tests := []struct {
		name   string
		answer ai.Answer
		want   string
	}{
		{
			name:   "a certain no keeps its probability",
			answer: ai.Answer{Name: "is_bug", Kind: ai.KindYesNo, Confidence: 1},
			want:   `{"confidence":1,"kind":"yes_no","name":"is_bug","probability":0}`,
		},
		{
			name: "a choice has no probability of yes",
			answer: ai.Answer{
				Name:                "team",
				Kind:                ai.KindChoice,
				Choice:              "payments",
				ChoiceProbabilities: map[string]float64{"payments": 1},
				Confidence:          1,
			},
			want: `{"choice":"payments","choice_probabilities":{"payments":1},"confidence":1,"kind":"choice","name":"team"}`,
		},
		{
			name: "the lowest level keeps its score",
			answer: ai.Answer{
				Name:               "urgency",
				Kind:               ai.KindScore,
				LevelProbabilities: []float64{1, 0},
				Confidence:         1,
			},
			want: `{"confidence":1,"kind":"score","level_probabilities":[1,0],"name":"urgency","score":0}`,
		},
		{
			name:   "a box",
			answer: ai.Answer{Name: "logo", Kind: ai.KindBox, Bounds: &ai.Bounds{Right: 1}, Confidence: 0.5},
			want:   `{"bounds":{"top":0,"bottom":0,"left":0,"right":1},"confidence":0.5,"kind":"box","name":"logo"}`,
		},
		{
			name:   "a refusal",
			answer: ai.Answer{Name: "is_new", Kind: ai.KindYesNo, Refused: true},
			want:   `{"kind":"yes_no","name":"is_new","refused":true}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.answer)
			require.NoError(t, err)
			assert.JSONEq(t, tt.want, string(data))

			var back ai.Answer
			require.NoError(t, json.Unmarshal(data, &back))
			assert.Equal(t, tt.answer, back)
		})
	}
}

func TestQuestion_JSON(t *testing.T) {
	questions := []ai.Question{
		ai.YesNo("is_bug", "Is it a defect?").YesWhen("Broken."),
		ai.Score("urgency", "How urgent?", later, now),
	}

	data, err := json.Marshal(questions)
	require.NoError(t, err)
	assert.JSONEq(t, `[
		{"name":"is_bug","kind":"yes_no","instructions":"Is it a defect?","yes":"Broken."},
		{"name":"urgency","kind":"score","instructions":"How urgent?",
		 "levels":[{"label":"later"},{"label":"now","description":"It blocks revenue."}]}
	]`, string(data))

	var back []ai.Question
	require.NoError(t, json.Unmarshal(data, &back))
	assert.Equal(t, questions, back)
}
