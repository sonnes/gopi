package openrouter_test

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/internal/providertest"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/openrouter"
)

// canonicalHost is the host in every recording.
const canonicalHost = "openrouter.ai"

// recordFrom is the API that a recorded test records from.
var recordFrom = providertest.Server{KeyEnv: "OPENROUTER_API_KEY"}

// newRecordedProvider returns a provider that replays the recording of
// the test.
func newRecordedProvider(t *testing.T) *openrouter.Provider {
	t.Helper()
	r := providertest.Open(t, recordFrom, canonicalHost)
	return openrouter.New(
		openrouter.APIKey(r.APIKey),
		openrouter.BaseURL(r.BaseURL),
		openrouter.HTTPClient(r.Client),
	)
}

// newRecordedDecider returns the decision model id that replays the
// recording of the test.
func newRecordedDecider(t *testing.T, id string) ai.DecisionModel {
	t.Helper()
	return ai.DecisionModel{Model: ai.Model{ID: id}, Provider: newRecordedProvider(t)}
}

// ticketInput is a support ticket about a defect in the checkout.
var ticketInput = []ai.Message{ai.UserMessage(
	"My checkout page shows a blank screen after I click Pay. I tried two browsers, and no customer can pay.",
)}

// askTicket asks a yes-or-no, a choice, and a score question about
// ticketInput, and checks the answers. A model can answer each one in
// another way, so the checks hold for each valid answer, and the clear
// ones hold for the ticket.
func askTicket(t *testing.T, dm ai.DecisionModel) {
	t.Helper()
	resp, err := dm.Decide(
		t.Context(),
		ticketInput,
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

func TestDecideRecorded_Jev(t *testing.T) {
	askTicket(t, newRecordedDecider(t, "typesafe/jev-1.14"))
}

func TestDecideRecorded_GPT6LunaDecisions(t *testing.T) {
	askTicket(t, newRecordedDecider(t, "openai/gpt-6-luna-decisions"))
}

// TestDecideRecorded_Transcript asks about the transcript of an agent,
// with a tool call and its result. The choice has bare option IDs.
func TestDecideRecorded_Transcript(t *testing.T) {
	dm := newRecordedDecider(t, "typesafe/jev-1.14")
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

// The size of the image of the box case. The red square is in its top
// right quarter. Jev rejects a small image with "Invalid image
// dimensions", so the image has 512 pixels on each side.
const (
	imageWidth  = 512
	imageHeight = 512
	squareSize  = 96
	squareLeft  = 384
	squareTop   = 32
)

// squareImage returns a white PNG with a red square in its top right.
func squareImage(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, imageWidth, imageHeight))
	red := color.RGBA{R: 255, A: 255}
	for y := range imageHeight {
		for x := range imageWidth {
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

func TestDecideRecorded_JevBox(t *testing.T) {
	dm := newRecordedDecider(t, "typesafe/jev-1.14")
	photo := ai.File{MediaType: "image/png", Data: squareImage(t)}
	input := []ai.Message{{Role: ai.RoleUser, Content: []ai.Content{photo}}}

	resp, err := dm.Decide(t.Context(), input, ai.Box("square", "Find the red square."))

	require.NoError(t, err)
	square, err := resp.Box("square")
	require.NoError(t, err)
	assert.Greater(t, square.Left, 0.5, "the square is in the right half")
	assert.Less(t, square.Bottom, 0.5, "the square is in the top half")
}
