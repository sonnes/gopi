package openai_test

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/openai"
)

// lunaDecider is the decision model of OpenAI, with the prices of
// models.json.
var lunaDecider = ai.Model{ID: "gpt-6-luna", Pricing: luna.Pricing}

// newRecordedDecider returns the decision model that replays the
// recording of the test.
func newRecordedDecider(t *testing.T) ai.DecisionModel {
	t.Helper()
	skipOnCompatibleAPI(t)
	return ai.DecisionModel{Model: lunaDecider, Provider: newTestProvider(t)}
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
	assert.Len(t, resp.Answers[1].ChoiceProbabilities, 2)

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

// cachedWords is the count of words in the input of the cache case. A
// prompt cache needs 1024 tokens or more.
const cachedWords = 1500

// TestDecideRecorded_Cache asks the same long input twice with one
// cache key. The second call reads the input from the cache.
func TestDecideRecorded_Cache(t *testing.T) {
	dm := newRecordedDecider(t)
	log := strings.Repeat("GET /checkout 500 ", cachedWords/3)
	input := []ai.Message{ai.UserMessage("Here is the access log of the checkout service:\n" + log)}
	ask := func() *ai.DecisionResponse {
		resp, err := dm.Decide(
			t.Context(),
			input,
			ai.YesNo("has_errors", "Does the log show server errors?"),
			openai.PromptCacheKey("decide-cache-case"),
		)
		require.NoError(t, err)
		return resp
	}

	ask()
	second := ask()

	assert.Positive(t, second.Usage.CacheRead, "the second call reads the cache")
}

// The size of the image of the image case. Each side has 512 pixels,
// and the red square has 96.
const (
	imageSide  = 512
	squareSide = 96
	squareLeft = 384
	squareTop  = 32
)

// squareImage returns a white JPEG with a red square in its top right.
// The Decisions API reads a PNG of this picture as all white, so the
// image is a JPEG.
func squareImage(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, imageSide, imageSide))
	red := color.RGBA{R: 255, A: 255}
	for y := range imageSide {
		for x := range imageSide {
			inSquare := x >= squareLeft && x < squareLeft+squareSide &&
				y >= squareTop && y < squareTop+squareSide
			if inSquare {
				img.Set(x, y, red)
				continue
			}
			img.Set(x, y, color.White)
		}
	}
	var buf bytes.Buffer
	require.NoError(t, jpeg.Encode(&buf, img, nil))
	return buf.Bytes()
}

func TestDecideRecorded_Image(t *testing.T) {
	dm := newRecordedDecider(t)
	photo := ai.File{MediaType: "image/jpeg", Data: squareImage(t)}
	input := []ai.Message{{
		Role: ai.RoleUser,
		Content: []ai.Content{
			ai.Text{Text: "Here is a picture of a shape."},
			photo,
		},
	}}

	resp, err := dm.Decide(t.Context(), input, ai.YesNo("has_red", "Is the shape in the picture red?"))

	require.NoError(t, err)
	hasRed, err := resp.YesNo("has_red")
	require.NoError(t, err)
	assert.Greater(t, hasRed, 0.5)
}
