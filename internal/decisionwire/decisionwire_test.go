package decisionwire

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sonnes/gopi/pkg/ai"
)

func TestAnswers(t *testing.T) {
	urgency := ai.Score(
		"urgency",
		"How urgent?",
		ai.ScoreLevel{Label: "later"},
		ai.ScoreLevel{Label: "now"},
	)
	bounds := &ai.Bounds{
		Top:    0.1,
		Bottom: 0.2,
		Left:   0.6,
		Right:  0.9,
	}
	tests := []struct {
		name   string
		answer Answer
		want   ai.Answer
	}{
		{
			name: "noul",
			answer: Answer{
				Type:       TypeNoul,
				Noul:       0.2,
				Confidence: 0.8,
			},
			want: ai.Answer{
				Kind:        ai.KindYesNo,
				Probability: 0.2,
				Confidence:  0.8,
			},
		},
		{
			name: "choice with probabilities",
			answer: Answer{
				Type:          TypeChoice,
				Choice:        "payments",
				Confidence:    0.26,
				Probabilities: map[string]float64{"payments": 0.8, "frontend": 0.2},
			},
			want: ai.Answer{
				Kind:                ai.KindChoice,
				Choice:              "payments",
				Confidence:          0.26,
				ChoiceProbabilities: map[string]float64{"payments": 0.8, "frontend": 0.2},
			},
		},
		{
			name: "choice with no probabilities",
			answer: Answer{
				Type:          TypeChoice,
				Choice:        "frontend",
				Probabilities: map[string]float64{},
			},
			want: ai.Answer{
				Kind:   ai.KindChoice,
				Choice: "frontend",
			},
		},
		{
			name: "score with probabilities by level index",
			answer: Answer{
				Type:          TypeScore,
				Score:         0.7,
				Confidence:    0.7,
				Probabilities: map[string]float64{"0": 0.3, "1": 0.7},
			},
			want: ai.Answer{
				Kind:               ai.KindScore,
				Score:              0.7,
				Confidence:         0.7,
				LevelProbabilities: []float64{0.3, 0.7},
			},
		},
		{
			name: "score ignores a key that is not a level index",
			answer: Answer{
				Type:          TypeScore,
				Score:         1,
				Probabilities: map[string]float64{"1": 0.9, "2": 0.1, "-1": 0.1, "now": 0.1},
			},
			want: ai.Answer{
				Kind:               ai.KindScore,
				Score:              1,
				LevelProbabilities: []float64{0, 0.9},
			},
		},
		{
			name: "score with no probabilities",
			answer: Answer{
				Type:  TypeScore,
				Score: 0.5,
			},
			want: ai.Answer{
				Kind:  ai.KindScore,
				Score: 0.5,
			},
		},
		{
			name: "box",
			answer: Answer{
				Type:       TypeBox,
				Confidence: 0.93,
				Box:        bounds,
			},
			want: ai.Answer{
				Kind:       ai.KindBox,
				Confidence: 0.93,
				Bounds:     bounds,
			},
		},
		{
			name:   "refusal has no kind",
			answer: Answer{Type: TypeRefusal},
			want:   ai.Answer{Refused: true},
		},
		{
			name: "unknown type is the kind",
			answer: Answer{
				Type:       "rank",
				Confidence: 0.4,
			},
			want: ai.Answer{
				Kind:       ai.QuestionKind("rank"),
				Confidence: 0.4,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := urgency.Name
			answers := map[string]Answer{name: tt.answer}

			got := Answers(answers, []ai.Question{urgency})

			want := tt.want
			want.Name = name
			assert.Equal(t, []ai.Answer{want}, got)
		})
	}
}

func TestAnswers_KeepsEachAnswer(t *testing.T) {
	answers := map[string]Answer{
		"is_bug": {Type: TypeNoul, Noul: 0.9},
		"is_new": {Type: TypeRefusal},
	}

	got := Answers(answers, nil)

	assert.ElementsMatch(t, []ai.Answer{
		{Name: "is_bug", Kind: ai.KindYesNo, Probability: 0.9},
		{Name: "is_new", Refused: true},
	}, got)
}

func TestLevelTexts(t *testing.T) {
	levels := []ai.ScoreLevel{
		{Label: "later"},
		{Label: "now", Description: "It blocks revenue."},
	}

	got := LevelTexts(levels)

	assert.Equal(t, []string{"later", "now: It blocks revenue."}, got)
}
