// Package decisionwire holds the wire format that the decisions APIs of
// OpenRouter and Gemini share: the question and answer types, the
// question, and the answer. A caller is a provider. It builds the
// criteria of each question itself, because the criteria differ between
// the APIs. It maps the answers with [Answers].
package decisionwire

import (
	"strconv"

	"github.com/sonnes/gopi/pkg/ai"
)

// The question and answer types of the decisions APIs.
const (
	TypeNoul    = "noul"
	TypeChoice  = "choice"
	TypeScore   = "score"
	TypeBox     = "box"
	TypeRefusal = "refusal"
)

// Question is one question of a request. Criteria is the JSON value
// that the API of the provider takes for the type.
type Question struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"`
}

// LevelTexts returns the criteria of a score question: one text for
// each level, lowest first. A text is the label, and the description
// after a colon if the level has one.
func LevelTexts(levels []ai.ScoreLevel) []string {
	out := make([]string, 0, len(levels))
	for _, l := range levels {
		if l.Description == "" {
			out = append(out, l.Label)
			continue
		}
		out = append(out, l.Label+": "+l.Description)
	}
	return out
}

// Answer is one answer of a response. The API keys Probabilities by the
// ID of an option for a choice answer, and by the index of a level for
// a score answer.
type Answer struct {
	Type          string             `json:"type"`
	Noul          float64            `json:"noul"`
	Choice        string             `json:"choice"`
	Score         float64            `json:"score"`
	Confidence    float64            `json:"confidence"`
	Probabilities map[string]float64 `json:"probabilities"`
	Box           *ai.Bounds         `json:"box"`
}

// Answers maps the answers, which the API keys by the name of the
// question. The kind of an answer comes from its type. A refusal has no
// kind, and an unknown type is the kind. The confidence is the reported
// confidence. Answers reads questions only for the number of levels of
// a score question. [ai.DecisionModel.Decide] orders and checks the
// answers.
func Answers(answers map[string]Answer, questions []ai.Question) []ai.Answer {
	levels := make(map[string]int, len(questions))
	for _, q := range questions {
		levels[q.Name] = len(q.Levels)
	}

	out := make([]ai.Answer, 0, len(answers))
	for name, a := range answers {
		answer := ai.Answer{
			Name:       name,
			Confidence: a.Confidence,
		}
		switch a.Type {
		case TypeNoul:
			answer.Kind = ai.KindYesNo
			answer.Probability = a.Noul
		case TypeChoice:
			answer.Kind = ai.KindChoice
			answer.Choice = a.Choice
			if len(a.Probabilities) > 0 {
				answer.ChoiceProbabilities = a.Probabilities
			}
		case TypeScore:
			answer.Kind = ai.KindScore
			answer.Score = a.Score
			answer.LevelProbabilities = levelProbabilities(a.Probabilities, levels[name])
		case TypeBox:
			answer.Kind = ai.KindBox
			answer.Bounds = a.Box
		case TypeRefusal:
			answer.Refused = true
		default:
			answer.Kind = ai.QuestionKind(a.Type)
		}
		out = append(out, answer)
	}
	return out
}

// levelProbabilities puts the probability of each level at its index.
// The API keys each probability by the index of its level. A key that
// is not an index of a level is ignored. It returns nil if the API
// reports none.
func levelProbabilities(ps map[string]float64, levels int) []float64 {
	if len(ps) == 0 {
		return nil
	}
	out := make([]float64, levels)
	for key, p := range ps {
		index, err := strconv.Atoi(key)
		if err == nil && index >= 0 && index < levels {
			out[index] = p
		}
	}
	return out
}
