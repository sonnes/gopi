package gemini_test

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
)

// recordedDecisionModel is the recorded decision model, with its price
// for 1M input tokens.
var recordedDecisionModel = ai.Model{
	ID:      "gemini-decision-1",
	Pricing: ai.Pricing{Input: 0.1},
}

// newRecordedDecider returns the decision model that replays the
// recording of the test.
func newRecordedDecider(t *testing.T) ai.DecisionModel {
	t.Helper()
	return ai.DecisionModel{
		Model:    recordedDecisionModel,
		Provider: newTestProvider(t),
	}
}

func TestDecideRecorded_Ticket(t *testing.T) {
	dm := newRecordedDecider(t)
	input := []ai.Message{ai.UserMessage(
		"My checkout page shows a blank screen after I click Pay. I tried two browsers, and no customer can pay.",
	)}

	resp, err := dm.Decide(
		t.Context(),
		input,
		ai.YesNo("is_bug", "Does the user report a software defect?").
			YesWhen("The user describes broken behavior.").
			NoWhen("The user asks a question."),
		ai.Choice(
			"team",
			"Which team owns this ticket?",
			ai.ChoiceOption{ID: "payments", Description: "Checkout, billing, or payments."},
			ai.ChoiceOption{ID: "account", Description: "Login, password, or profile."},
		),
		ai.Score(
			"urgency",
			"How urgent is this ticket?",
			ai.ScoreLevel{Label: "later", Description: "It can wait."},
			ai.ScoreLevel{Label: "this week", Description: "Fix it this week."},
			ai.ScoreLevel{Label: "now", Description: "It blocks revenue now."},
		),
	)
	require.NoError(t, err)

	isBug, err := resp.YesNo("is_bug")
	require.NoError(t, err)
	assert.Greater(t, isBug, 0.5, "the ticket reports a defect")

	team, err := resp.Choice("team")
	require.NoError(t, err)
	assert.Equal(t, "payments", team)
	ps := resp.Answers[1].ChoiceProbabilities
	assert.InDelta(t, 1, ps["payments"]+ps["account"], 0.02)

	urgency, err := resp.Score("urgency")
	require.NoError(t, err)
	assert.Greater(t, urgency, 1.0, "the ticket blocks revenue")
	require.Len(t, resp.Answers[2].LevelProbabilities, 3)
	assert.Greater(t, resp.Answers[2].LevelProbabilities[2], 0.5)

	assert.Positive(t, resp.Usage.Input)
	assert.Positive(t, resp.Usage.Cost.Total())
}

// TestDecideRecorded_Transcript asks about the transcript of an agent,
// with a tool call and its result. The choice has bare option IDs.
func TestDecideRecorded_Transcript(t *testing.T) {
	dm := newRecordedDecider(t)
	input := []ai.Message{
		ai.UserMessage("Delete the temp files in /tmp/build."),
		ai.AssistantMessage(
			ai.Text{Text: "I will delete them."},
			ai.ToolCall{ID: "c1", Name: "bash", Input: json.RawMessage(`{"command":"rm -rf /tmp/build/*.tmp"}`)},
		),
		ai.ToolResultMessage("c1", ai.Text{Text: "Removed 12 files."}),
		ai.AssistantMessage(ai.Text{Text: "I removed 12 temp files from /tmp/build."}),
	}

	resp, err := dm.Decide(
		t.Context(),
		input,
		ai.YesNo("done", "Did the assistant report that it removed the files?"),
		ai.Choice(
			"tool",
			"Which tool did the agent call?",
			ai.ChoiceOption{ID: "bash"},
			ai.ChoiceOption{ID: "browser"},
			ai.ChoiceOption{ID: "editor"},
		),
	)

	require.NoError(t, err)
	done, err := resp.YesNo("done")
	require.NoError(t, err)
	assert.Greater(t, done, 0.5)
	tool, err := resp.Choice("tool")
	require.NoError(t, err)
	assert.Equal(t, "bash", tool)
}

// The size of the image of the image case, with a red square in its
// top right quarter.
const (
	squareImageSize = 256
	squareSize      = 64
	squareLeft      = 176
	squareTop       = 16
)

// squareImage returns a white PNG with a red square in its top right.
func squareImage(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, squareImageSize, squareImageSize))
	red := color.RGBA{R: 255, A: 255}
	for y := range squareImageSize {
		for x := range squareImageSize {
			inSquare := x >= squareLeft && x < squareLeft+squareSize &&
				y >= squareTop && y < squareTop+squareSize
			if inSquare {
				img.Set(x, y, red)
				continue
			}
			img.Set(x, y, color.White)
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func TestDecideRecorded_Image(t *testing.T) {
	dm := newRecordedDecider(t)
	photo := ai.File{MediaType: "image/png", Data: squareImage(t)}
	input := []ai.Message{{
		Role:    ai.RoleUser,
		Content: []ai.Content{ai.Text{Text: "Here is a picture."}, photo},
	}}

	resp, err := dm.Decide(
		t.Context(),
		input,
		ai.YesNo("has_square", "Does the picture show a square?"),
		ai.Choice(
			"color",
			"What color is the shape in the picture?",
			ai.ChoiceOption{ID: "red"},
			ai.ChoiceOption{ID: "green"},
			ai.ChoiceOption{ID: "blue"},
		),
	)

	require.NoError(t, err)
	hasSquare, err := resp.YesNo("has_square")
	require.NoError(t, err)
	assert.Greater(t, hasSquare, 0.5)
	shape, err := resp.Choice("color")
	require.NoError(t, err)
	assert.Equal(t, "red", shape)
}
