package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// DecisionProvider answers closed questions about one input. The model
// scores the possible answers and returns their probabilities. It
// generates no text.
//
// [DecisionModel.Decide] checks the input and the questions before it
// calls Decide, and checks the answers after. A provider maps the wire
// format. It returns one answer for each question that the API answers,
// in any order, with the kind of the answer that the API gives. It can
// leave the kind of a refusal empty and the confidence of a [KindYesNo]
// answer zero, because Decide fills them.
type DecisionProvider interface {
	Decide(
		ctx context.Context,
		model Model,
		input []Message,
		questions []Question,
		opts Options,
	) (*DecisionResponse, error)
}

// ErrInvalidDecision is the error of a call of [DecisionModel.Decide]
// that breaks a rule of the input or of the questions. The provider
// sends nothing.
var ErrInvalidDecision = errors.New("ai: invalid decision request")

// ErrNoAnswer is the error of a question with no answer of its kind:
// the API left the question out, the answer has another kind, or a
// caller asks for a name that the response does not have.
var ErrNoAnswer = errors.New("ai: no answer")

// ErrRefused is the error of an accessor of [DecisionResponse] for a
// question that the model refused.
var ErrRefused = errors.New("ai: the model refused the question")

// QuestionKind is the kind of a [Question]. It sets the answer that the
// model gives.
type QuestionKind string

const (
	// KindYesNo asks if a condition holds. The answer is the probability
	// of yes.
	KindYesNo QuestionKind = "yes_no"
	// KindChoice picks one of the options.
	KindChoice QuestionKind = "choice"
	// KindScore puts the input on an ordered scale of levels.
	KindScore QuestionKind = "score"
	// KindBox finds one target in an image of the input. Only some
	// decision models answer it.
	KindBox QuestionKind = "box"
)

// The limits that each decisions API sets.
const (
	// maxNameLength is the most characters in a question name.
	maxNameLength = 64
	// maxOptionIDLength is the most characters in the ID of an option.
	maxOptionIDLength = 128
	// minOptions is the fewest options of a choice question.
	minOptions = 2
	// minLevels is the fewest levels of a score question.
	minLevels = 2
)

// yesThreshold is the probability from which [Answer.Yes] is true.
const yesThreshold = 0.5

// imageMediaPrefix starts the media type of an image.
const imageMediaPrefix = "image/"

// namePattern matches a legal question name. One decisions API allows
// only these characters, so a name works on each API.
var namePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// Question is one closed question about the input of a decision. Name
// is the key of its answer. The model does not see the name. Build a
// question with [YesNo], [Choice], [Score], or [Box].
//
// A Question is also an [Options] value, so the questions and the
// options of a call of [DecisionModel.Decide] go in one list.
// [JoinOptions] keeps each question, in order. [QuestionsOf] reads them.
type Question struct {
	Name         string       `json:"name"`
	Kind         QuestionKind `json:"kind"`
	Instructions string       `json:"instructions"`
	// Yes and No describe the two outcomes of a [KindYesNo] question.
	// Both are optional.
	Yes string `json:"yes,omitempty"`
	No  string `json:"no,omitempty"`
	// Options are the options of a [KindChoice] question.
	Options []ChoiceOption `json:"options,omitempty"`
	// Levels are the levels of a [KindScore] question, lowest first.
	Levels []ScoreLevel `json:"levels,omitempty"`
}

func (Question) options() {}

// ChoiceOption is one option of a [KindChoice] question. ID comes back
// in [Answer.Choice]. Description is optional. Leave it empty if the ID
// explains itself.
type ChoiceOption struct {
	ID          string `json:"id"`
	Description string `json:"description,omitempty"`
}

// ScoreLevel is one level of a [KindScore] question. Label names the
// level. Description is optional. An API with only one text for a
// level gets "Label: Description".
type ScoreLevel struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// YesNo returns a [KindYesNo] question.
func YesNo(name, instructions string) Question {
	return Question{
		Name:         name,
		Kind:         KindYesNo,
		Instructions: instructions,
	}
}

// YesWhen returns q with a description of the yes outcome.
func (q Question) YesWhen(description string) Question {
	q.Yes = description
	return q
}

// NoWhen returns q with a description of the no outcome.
func (q Question) NoWhen(description string) Question {
	q.No = description
	return q
}

// Choice returns a [KindChoice] question with options.
func Choice(name, instructions string, options ...ChoiceOption) Question {
	return Question{
		Name:         name,
		Kind:         KindChoice,
		Instructions: instructions,
		Options:      options,
	}
}

// Score returns a [KindScore] question with levels, lowest first.
func Score(name, instructions string, levels ...ScoreLevel) Question {
	return Question{
		Name:         name,
		Kind:         KindScore,
		Instructions: instructions,
		Levels:       levels,
	}
}

// Box returns a [KindBox] question. The instructions name the target in
// an image of the input.
func Box(name, instructions string) Question {
	return Question{
		Name:         name,
		Kind:         KindBox,
		Instructions: instructions,
	}
}

// QuestionsOf returns each [Question] in opts, in order.
func QuestionsOf(opts Options) []Question {
	switch v := opts.(type) {
	case Question:
		return []Question{v}
	case option:
		return questionOf(v)
	case set:
		var out []Question
		for _, o := range v {
			out = append(out, questionOf(o)...)
		}
		return out
	default:
		return nil
	}
}

func questionOf(o option) []Question {
	if o.key != &questionKey {
		return nil
	}
	return []Question{o.value.(Question)}
}

// validateDecision returns an error that wraps [ErrInvalidDecision] if
// the input or the questions break a rule that each decisions API has:
//
//   - The input has one message or more, and there is one question or
//     more.
//   - Each name has 1 to 64 characters from [A-Za-z0-9_-], and no two
//     questions have the same name.
//   - Each question has a known kind and instructions, and no field of
//     another kind.
//   - A choice question has 2 or more options, with unique IDs of 1 to
//     128 characters.
//   - A score question has 2 or more levels, with unique labels that are
//     not empty.
//   - A box question has an image in the input.
//
// A provider leaves its own limits to its API, such as the most
// questions of one request.
func validateDecision(input []Message, questions []Question) error {
	if len(input) == 0 {
		return invalid("no input")
	}
	if len(questions) == 0 {
		return invalid("no questions")
	}
	names := make(map[string]bool, len(questions))
	for _, q := range questions {
		if err := q.validate(input); err != nil {
			return err
		}
		if names[q.Name] {
			return invalid("question %q twice", q.Name)
		}
		names[q.Name] = true
	}
	return nil
}

func (q Question) validate(input []Message) error {
	legalName := len(q.Name) <= maxNameLength && namePattern.MatchString(q.Name)
	if !legalName {
		return invalid("name %q: use 1 to %d characters from [A-Za-z0-9_-]", q.Name, maxNameLength)
	}
	if q.Instructions == "" {
		return invalid("question %q has no instructions", q.Name)
	}
	switch q.Kind {
	case KindYesNo, KindChoice, KindScore, KindBox:
	default:
		return invalid("question %q has the unknown kind %q", q.Name, q.Kind)
	}
	if err := q.validateFields(); err != nil {
		return err
	}
	switch q.Kind {
	case KindChoice:
		return q.validateOptions()
	case KindScore:
		return q.validateLevels()
	case KindBox:
		if !hasImage(input) {
			return invalid("box %q needs an image in the input", q.Name)
		}
	}
	return nil
}

// validateFields rejects a field that only another kind takes, because
// each provider drops it.
func (q Question) validateFields() error {
	hasOutcomes := q.Yes != "" || q.No != ""
	switch {
	case hasOutcomes && q.Kind != KindYesNo:
		return invalid("%s %q has outcomes, which only a yes-or-no question takes", q.Kind, q.Name)
	case len(q.Options) > 0 && q.Kind != KindChoice:
		return invalid("%s %q has options, which only a choice question takes", q.Kind, q.Name)
	case len(q.Levels) > 0 && q.Kind != KindScore:
		return invalid("%s %q has levels, which only a score question takes", q.Kind, q.Name)
	}
	return nil
}

func (q Question) validateOptions() error {
	if len(q.Options) < minOptions {
		return invalid("choice %q needs %d options or more", q.Name, minOptions)
	}
	ids := make(map[string]bool, len(q.Options))
	for _, o := range q.Options {
		if o.ID == "" || len(o.ID) > maxOptionIDLength {
			return invalid("option ID of choice %q: use 1 to %d characters", q.Name, maxOptionIDLength)
		}
		if ids[o.ID] {
			return invalid("choice %q has the option %q twice", q.Name, o.ID)
		}
		ids[o.ID] = true
	}
	return nil
}

func (q Question) validateLevels() error {
	if len(q.Levels) < minLevels {
		return invalid("score %q needs %d levels or more", q.Name, minLevels)
	}
	labels := make(map[string]bool, len(q.Levels))
	for _, l := range q.Levels {
		if l.Label == "" {
			return invalid("score %q has a level with no label", q.Name)
		}
		if labels[l.Label] {
			return invalid("score %q has the level %q twice", q.Name, l.Label)
		}
		labels[l.Label] = true
	}
	return nil
}

// hasImage reports whether a message of input holds an image.
func hasImage(input []Message) bool {
	for _, msg := range input {
		for _, c := range msg.Content {
			f, ok := c.(File)
			if ok && isImage(f) {
				return true
			}
		}
	}
	return false
}

func isImage(f File) bool {
	return strings.HasPrefix(f.MediaType, imageMediaPrefix)
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrInvalidDecision}, args...)...)
}

// DecisionResponse holds the answer to each question of a decision.
type DecisionResponse struct {
	// Answers holds one answer for each question, in the order of the
	// questions.
	Answers []Answer `json:"answers"`
	// Usage counts the tokens. A decision generates no text, but an API
	// can report output tokens.
	Usage Usage `json:"usage"`
}

// Answer returns the answer to the question name, and false if the
// response has none.
func (r *DecisionResponse) Answer(name string) (Answer, bool) {
	for _, a := range r.Answers {
		if a.Name == name {
			return a, true
		}
	}
	return Answer{}, false
}

// YesNo returns the probability of yes of the [KindYesNo] question
// name. The error wraps [ErrRefused] or [ErrNoAnswer].
func (r *DecisionResponse) YesNo(name string) (float64, error) {
	a, err := r.answerOf(name, KindYesNo)
	return a.Probability, err
}

// Choice returns the ID of the chosen option of the [KindChoice]
// question name. The error wraps [ErrRefused] or [ErrNoAnswer].
func (r *DecisionResponse) Choice(name string) (string, error) {
	a, err := r.answerOf(name, KindChoice)
	return a.Choice, err
}

// Score returns the score of the [KindScore] question name. The error
// wraps [ErrRefused] or [ErrNoAnswer].
func (r *DecisionResponse) Score(name string) (float64, error) {
	a, err := r.answerOf(name, KindScore)
	return a.Score, err
}

// Box returns the box of the [KindBox] question name. The error wraps
// [ErrRefused] or [ErrNoAnswer].
func (r *DecisionResponse) Box(name string) (Bounds, error) {
	a, err := r.answerOf(name, KindBox)
	if err != nil {
		return Bounds{}, err
	}
	if a.Bounds == nil {
		return Bounds{}, fmt.Errorf("%w: the box %q has no bounds", ErrNoAnswer, name)
	}
	return *a.Bounds, nil
}

func (r *DecisionResponse) answerOf(name string, kind QuestionKind) (Answer, error) {
	a, ok := r.Answer(name)
	switch {
	case !ok:
		return Answer{}, fmt.Errorf("%w: no answer to the question %q", ErrNoAnswer, name)
	case a.Kind != kind:
		return Answer{}, fmt.Errorf("%w: %q is a %s answer, not a %s answer", ErrNoAnswer, name, a.Kind, kind)
	case a.Refused:
		return Answer{}, fmt.Errorf("%w: %q", ErrRefused, name)
	}
	return a, nil
}

// Answer is the answer to one [Question]. Kind sets the fields that
// hold the answer. The JSON of an answer has only the fields of its
// kind, and a zero value of its kind stays in the JSON.
type Answer struct {
	// Name is the name of the question.
	Name string       `json:"name"`
	Kind QuestionKind `json:"kind"`
	// Refused reports that the model gave no answer to this question.
	// The other answers of the decision stay.
	Refused bool `json:"refused"`
	// Confidence is the confidence of the model in the answer, from 0
	// to 1, as the API reports it. It can differ from the top
	// probability. If the API reports none for a [KindYesNo] answer,
	// [DecisionModel.Decide] sets the larger of the probability and its
	// complement.
	Confidence float64 `json:"confidence"`
	// Probability is the probability of yes, for [KindYesNo].
	Probability float64 `json:"probability"`
	// Choice is the ID of the chosen option, for [KindChoice].
	Choice string `json:"choice"`
	// ChoiceProbabilities holds the probability of each option by its
	// ID, for [KindChoice]. It is nil if the API reports none.
	ChoiceProbabilities map[string]float64 `json:"choice_probabilities"`
	// Score is the level index, weighted by the probability of each
	// level, for [KindScore]. It can fall between two levels.
	Score float64 `json:"score"`
	// LevelProbabilities holds the probability of each level, lowest
	// first, for [KindScore]. It is nil if the API reports none.
	LevelProbabilities []float64 `json:"level_probabilities"`
	// Bounds is the box of the target, for [KindBox]. If the input has
	// more than one image, the box does not say which image it is in.
	// No API can answer that the target is absent.
	Bounds *Bounds `json:"bounds"`
}

// Yes reports whether the probability of yes of a [KindYesNo] answer is
// 0.5 or more.
func (a Answer) Yes() bool {
	return a.Probability >= yesThreshold
}

// MarshalJSON writes the name, the kind, and the fields of the kind of
// a. A refusal has no other field.
func (a Answer) MarshalJSON() ([]byte, error) {
	out := map[string]any{
		"name": a.Name,
		"kind": a.Kind,
	}
	if a.Refused {
		out["refused"] = true
		return json.Marshal(out)
	}
	out["confidence"] = a.Confidence
	switch a.Kind {
	case KindYesNo:
		out["probability"] = a.Probability
	case KindChoice:
		out["choice"] = a.Choice
		if a.ChoiceProbabilities != nil {
			out["choice_probabilities"] = a.ChoiceProbabilities
		}
	case KindScore:
		out["score"] = a.Score
		if a.LevelProbabilities != nil {
			out["level_probabilities"] = a.LevelProbabilities
		}
	case KindBox:
		if a.Bounds != nil {
			out["bounds"] = a.Bounds
		}
	}
	return json.Marshal(out)
}

// Bounds is a box in an image. Each edge is a fraction of the image
// height or width, from 0 at the top or left to 1 at the bottom or
// right.
type Bounds struct {
	Top    float64 `json:"top"`
	Bottom float64 `json:"bottom"`
	Left   float64 `json:"left"`
	Right  float64 `json:"right"`
}

// DecisionModel is a [Model] bound to a [DecisionProvider].
type DecisionModel struct {
	Model    Model
	Provider DecisionProvider
}

// Decide asks the questions in opts about input. Each [Question] is an
// option, so the questions and the other options go in one list:
//
//	resp, err := dm.Decide(ctx, input,
//		ai.YesNo("is_bug", "Does the user report a defect?"),
//		ai.MaxRetries(2),
//	)
//
// Decide checks the input and the questions first, so the provider gets
// no invalid request. The error of a broken rule wraps
// [ErrInvalidDecision]. If the provider cannot send a question kind or
// a file, its error wraps [errors.ErrUnsupported]. Then Decide checks
// that each question has an answer of its kind, or a refusal. The error
// of a missing answer wraps [ErrNoAnswer]. The answers of the response
// are in the order of the questions.
func (dm DecisionModel) Decide(
	ctx context.Context,
	input []Message,
	opts ...Options,
) (*DecisionResponse, error) {
	joined := JoinOptions(opts...)
	questions := QuestionsOf(joined)
	if err := validateDecision(input, questions); err != nil {
		return nil, err
	}
	resp, err := dm.Provider.Decide(ctx, dm.Model, input, questions, joined)
	if err != nil {
		return nil, err
	}
	answers, err := normalize(questions, resp.Answers)
	if err != nil {
		return nil, err
	}
	resp.Answers = answers
	return resp, nil
}

// normalize returns one answer for each question, in the order of the
// questions. A refusal gets the kind of its question and keeps no other
// field. A [KindYesNo] answer with no confidence gets the probability of
// its top outcome. A [KindBox] answer must have bounds.
func normalize(questions []Question, answers []Answer) ([]Answer, error) {
	byName := make(map[string]Answer, len(answers))
	for _, a := range answers {
		byName[a.Name] = a
	}
	out := make([]Answer, 0, len(questions))
	for _, q := range questions {
		a, ok := byName[q.Name]
		if !ok {
			return nil, fmt.Errorf("%w: no answer to the question %q", ErrNoAnswer, q.Name)
		}
		if a.Refused {
			out = append(out, Answer{
				Name:    a.Name,
				Kind:    q.Kind,
				Refused: true,
			})
			continue
		}
		if a.Kind != q.Kind {
			return nil, fmt.Errorf(
				"%w: the answer to the %s question %q is a %s answer",
				ErrNoAnswer,
				q.Kind,
				q.Name,
				a.Kind,
			)
		}
		if a.Kind == KindYesNo && a.Confidence == 0 {
			a.Confidence = max(a.Probability, 1-a.Probability)
		}
		if a.Kind == KindBox && a.Bounds == nil {
			return nil, fmt.Errorf("%w: the box question %q has no bounds", ErrNoAnswer, q.Name)
		}
		out = append(out, a)
	}
	return out, nil
}
