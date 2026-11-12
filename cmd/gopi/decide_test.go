package main

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/catalog"
)

// judge is a text provider that also decides. It keeps the last
// request, and answers with resp.
type judge struct {
	lister
	input     []ai.Message
	questions []ai.Question
	resp      *ai.DecisionResponse
}

func (j *judge) Decide(
	_ context.Context,
	_ ai.Model,
	input []ai.Message,
	questions []ai.Question,
	_ ai.Options,
) (*ai.DecisionResponse, error) {
	j.input = input
	j.questions = questions
	return j.resp, nil
}

func judgeDetector(j *judge) catalog.Detector {
	return catalog.Detector{
		Provider: "judge",
		Source:   "JUDGE_KEY",
		Models:   []ai.Model{{ID: "jev"}},
		Detect: func() (ai.TextProvider, bool) {
			return j, true
		},
	}
}

var judged = &ai.DecisionResponse{
	Answers: []ai.Answer{
		{
			Name:        "is_bug",
			Kind:        ai.KindYesNo,
			Probability: 0.91,
			Confidence:  0.91,
		},
		{
			Name:    "is_new",
			Kind:    ai.KindYesNo,
			Refused: true,
		},
		{
			Name:       "team",
			Kind:       ai.KindChoice,
			Choice:     "payments",
			Confidence: 0.84,
		},
		{
			Name:       "urgency",
			Kind:       ai.KindScore,
			Score:      1.73,
			Confidence: 0.78,
		},
		{
			Name:       "logo",
			Kind:       ai.KindBox,
			Confidence: 0.93,
			Bounds: &ai.Bounds{
				Top:    0.08,
				Bottom: 0.21,
				Left:   0.62,
				Right:  0.94,
			},
		},
	},
	Usage: ai.Usage{
		Input:     200,
		Output:    63,
		CacheRead: 12,
		Cost:      ai.Cost{Input: 0.000021},
	},
}

// writePNG writes the header of a PNG to a file and returns its path.
func writePNG(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "photo.png")
	require.NoError(t, os.WriteFile(path, []byte("\x89PNG\r\n\x1a\n"), 0o600))
	return path
}

func TestRunDecide_Table(t *testing.T) {
	path := writePNG(t)
	png, err := os.ReadFile(path)
	require.NoError(t, err)
	j := &judge{resp: judged}
	opts := decideOptions{
		model:  "judge/jev",
		yesNo:  []string{"is_bug=Is it a defect?", "is_new=Is it new?"},
		choice: []string{"team=Which team: the owner?|payments| frontend"},
		score:  []string{"urgency=How urgent?|It can wait, no rush|this week|now"},
		box:    []string{"logo=Find the logo."},
		images: []string{path},
	}
	var out strings.Builder

	err = runDecide(
		t.Context(),
		&out,
		[]catalog.Detector{judgeDetector(j)},
		opts,
		"Pay shows a blank page.",
	)

	require.NoError(t, err)
	assert.Equal(t, []ai.Message{{
		Role: ai.RoleUser,
		Content: []ai.Content{
			ai.Text{Text: "Pay shows a blank page."},
			ai.File{
				MediaType: "image/png",
				Data:      png,
			},
		},
	}}, j.input)
	assert.Equal(t, []ai.Question{
		ai.YesNo("is_bug", "Is it a defect?"),
		ai.YesNo("is_new", "Is it new?"),
		ai.Choice(
			"team",
			"Which team: the owner?",
			ai.ChoiceOption{ID: "payments"},
			ai.ChoiceOption{ID: "frontend"},
		),
		ai.Score(
			"urgency",
			"How urgent?",
			ai.ScoreLevel{Label: "It can wait, no rush"},
			ai.ScoreLevel{Label: "this week"},
			ai.ScoreLevel{Label: "now"},
		),
		ai.Box("logo", "Find the logo."),
	}, j.questions)
	assert.Equal(t, strings.Join([]string{
		"is_bug   yes                                        0.91",
		"is_new   refused                                    -",
		"team     payments                                   0.84",
		"urgency  1.73 of 2, now                             0.78",
		"logo     top 0.08 bottom 0.21 left 0.62 right 0.94  0.93",
		"in=200 out=63 cache_read=12 $0.000021",
		"",
	}, "\n"), out.String())
}

// TestRunDecide_JSON reads the questions from a file and the flags, and
// writes the answers as JSON.
func TestRunDecide_JSON(t *testing.T) {
	file := filepath.Join(t.TempDir(), "q.json")
	data := `[{"name":"is_bug","kind":"yes_no","instructions":"Is it a defect?","yes":"Broken."}]`
	require.NoError(t, os.WriteFile(file, []byte(data), 0o600))
	certainNo := ai.Answer{
		Name:       "is_bug",
		Kind:       ai.KindYesNo,
		Confidence: 1,
	}
	refused := ai.Answer{
		Name:    "is_new",
		Kind:    ai.KindYesNo,
		Refused: true,
	}
	j := &judge{resp: &ai.DecisionResponse{Answers: []ai.Answer{certainNo, refused}}}
	opts := decideOptions{
		model:     "judge/jev",
		json:      true,
		questions: file,
		yesNo:     []string{"is_new=Is it new?"},
	}
	var out strings.Builder

	err := runDecide(t.Context(), &out, []catalog.Detector{judgeDetector(j)}, opts, "Pay is blank.")

	require.NoError(t, err)
	assert.Equal(t, []ai.Question{
		ai.YesNo("is_bug", "Is it a defect?").YesWhen("Broken."),
		ai.YesNo("is_new", "Is it new?"),
	}, j.questions, "the questions of the file come first")
	assert.Contains(t, out.String(), `"probability":0`, "a certain no keeps its probability")
	var got ai.DecisionResponse
	require.NoError(t, json.Unmarshal([]byte(out.String()), &got))
	assert.Equal(t, []ai.Answer{certainNo, refused}, got.Answers)
}

func TestRunDecide_Errors(t *testing.T) {
	yesNo := []string{"is_bug=Is it a defect?"}
	tests := []struct {
		name    string
		opts    decideOptions
		input   string
		wantIs  error
		wantErr string
	}{
		{
			name:    "no model",
			opts:    decideOptions{yesNo: yesNo},
			input:   "Pay is blank.",
			wantErr: "no model",
		},
		{
			name:    "no input",
			opts:    decideOptions{model: "judge/jev", yesNo: yesNo},
			wantErr: "no input",
		},
		{
			name:    "no question",
			opts:    decideOptions{model: "judge/jev"},
			input:   "Pay is blank.",
			wantErr: "no question",
		},
		{
			name:    "no name",
			opts:    decideOptions{model: "judge/jev", yesNo: []string{"Is it a defect?"}},
			input:   "Pay is blank.",
			wantErr: "--yesno",
		},
		{
			name:    "a choice with no options",
			opts:    decideOptions{model: "judge/jev", choice: []string{"team=Which team?"}},
			input:   "Pay is blank.",
			wantErr: "--choice",
		},
		{
			name:    "a missing questions file",
			opts:    decideOptions{model: "judge/jev", questions: filepath.Join(t.TempDir(), "none.json")},
			input:   "Pay is blank.",
			wantIs:  fs.ErrNotExist,
			wantErr: "--questions",
		},
		{
			name:   "a model with no decisions",
			opts:   decideOptions{model: "a/alpha", yesNo: yesNo},
			input:  "Pay is blank.",
			wantIs: errors.ErrUnsupported,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ds := []catalog.Detector{
				judgeDetector(&judge{resp: judged}),
				detector("a", true, ai.Model{ID: "alpha"}),
			}

			err := runDecide(t.Context(), &strings.Builder{}, ds, tt.opts, tt.input)

			require.Error(t, err)
			if tt.wantIs != nil {
				assert.ErrorIs(t, err, tt.wantIs)
			}
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
			}
		})
	}
}

func TestDecideCommand_Flags(t *testing.T) {
	var got decideOptions
	root := rootCommand()
	decide := root.Command("decide")
	require.NotNil(t, decide)
	decide.Action = func(_ context.Context, c *cli.Command) error {
		got = decideOptionsOf(c)
		return nil
	}

	err := root.Run(t.Context(), []string{
		"gopi", "decide",
		"-m", "openai/gpt-6-luna",
		"--json",
		"--choice", "team=Which team, or teams?|payments|frontend",
		"--choice", "tier=Which tier?|free|pro",
		"--image", "a.png",
		"Pay is blank.",
	})

	require.NoError(t, err)
	assert.Equal(t, decideOptions{
		model: "openai/gpt-6-luna",
		json:  true,
		yesNo: []string{},
		score: []string{},
		box:   []string{},
		choice: []string{
			"team=Which team, or teams?|payments|frontend",
			"tier=Which tier?|free|pro",
		},
		images: []string{"a.png"},
	}, got)
}
