package askquestion

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/tool"
)

// runTimeout is the time that a test waits for Run to return.
const runTimeout = 5 * time.Second

// fakeResponder records the call that it receives and returns answers
// and err.
type fakeResponder struct {
	answers []Answer
	err     error

	id        string
	questions []Question
}

func (f *fakeResponder) Ask(_ context.Context, id string, questions []Question) ([]Answer, error) {
	f.id = id
	f.questions = questions
	return f.answers, f.err
}

// blockingResponder returns only after the caller cancels the context. A
// surface that never shows the questions behaves this way.
type blockingResponder struct{}

func (blockingResponder) Ask(ctx context.Context, _ string, _ []Question) ([]Answer, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func validInput() Input {
	return Input{Questions: []Question{{
		Header:   "Library",
		Question: "Which library should we use?",
		Options: []Option{
			{Label: "date-fns", Description: "Modular."},
			{Label: "dayjs", Description: "Small."},
		},
	}}}
}

func toolCall(t *testing.T, in Input) ai.ToolCall {
	t.Helper()

	raw, err := json.Marshal(in)
	require.NoError(t, err)

	return ai.ToolCall{
		ID:    "call-1",
		Name:  Name,
		Input: raw,
	}
}

// call runs tl with in and returns the result and its text.
func call(t *testing.T, tl tool.Tool, in Input) (ai.ToolResult, string) {
	t.Helper()

	res, err := tl.Run(t.Context(), toolCall(t, in))
	require.NoError(t, err)

	return res, resultText(t, res)
}

func resultText(t *testing.T, res ai.ToolResult) string {
	t.Helper()

	require.Len(t, res.Content, 1)
	text, ok := res.Content[0].(ai.Text)
	require.True(t, ok)
	return text.Text
}

func TestInfo(t *testing.T) {
	info := New(&fakeResponder{}).Info()

	assert.Equal(t, "ask_user_question", info.Name)
	assert.Equal(t, Description, info.Description)
	require.NotNil(t, info.InputSchema)
	assert.Contains(t, info.InputSchema.Properties, "questions")
	assert.True(t, info.Sequential, "a question holds the other calls of its message")
}

func TestRun_ReturnsTheAnswers(t *testing.T) {
	responder := &fakeResponder{answers: []Answer{{
		Header: "Library",
		Answer: "date-fns",
	}}}

	res, text := call(t, New(responder), validInput())

	require.False(t, res.IsError, text)
	assert.Equal(t, "call-1", responder.id)
	assert.Equal(t, validInput().Questions, responder.questions)

	var out Output
	require.NoError(t, json.Unmarshal([]byte(text), &out))
	assert.Equal(t, responder.answers, out.Answers)
}

func TestRun_RejectsInvalidQuestions(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Input)
		want   string
	}{
		{
			name: "no questions",
			change: func(in *Input) {
				in.Questions = nil
			},
			want: "at least 1",
		},
		{
			name: "too many questions",
			change: func(in *Input) {
				question := in.Questions[0]
				in.Questions = []Question{question, question, question, question, question}
				for i := range in.Questions {
					in.Questions[i].Header += string(rune('a' + i))
				}
			},
			want: "at most 4",
		},
		{
			name: "question is not complete",
			change: func(in *Input) {
				in.Questions[0].Question = "Choose a library"
			},
			want: "question mark",
		},
		{
			name: "empty header",
			change: func(in *Input) {
				in.Questions[0].Header = ""
			},
			want: "header is required",
		},
		{
			name: "long header",
			change: func(in *Input) {
				in.Questions[0].Header = strings.Repeat("x", HeaderMaxLen+1)
			},
			want: "header must be",
		},
		{
			name: "duplicate headers",
			change: func(in *Input) {
				in.Questions = append(in.Questions, in.Questions[0])
			},
			want: "headers must be unique",
		},
		{
			name: "duplicate questions",
			change: func(in *Input) {
				question := in.Questions[0]
				question.Header = "Other"
				in.Questions = append(in.Questions, question)
			},
			want: "questions must be unique",
		},
		{
			name: "too few options",
			change: func(in *Input) {
				in.Questions[0].Options = in.Questions[0].Options[:1]
			},
			want: "at least 2",
		},
		{
			name: "too many options",
			change: func(in *Input) {
				in.Questions[0].Options = []Option{
					{Label: "a"},
					{Label: "b"},
					{Label: "c"},
					{Label: "d"},
					{Label: "e"},
				}
			},
			want: "at most 4",
		},
		{
			name: "empty option",
			change: func(in *Input) {
				in.Questions[0].Options[0].Label = ""
			},
			want: "must not be empty",
		},
		{
			name: "duplicate options",
			change: func(in *Input) {
				in.Questions[0].Options[1].Label = in.Questions[0].Options[0].Label
			},
			want: "must be unique",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := validInput()
			tc.change(&in)

			res, text := call(t, New(&fakeResponder{}), in)

			assert.True(t, res.IsError)
			assert.Contains(t, text, tc.want)
			assert.Contains(t, text, "Expected shape")
		})
	}
}

func TestRun_RejectsInvalidJSON(t *testing.T) {
	res, err := New(&fakeResponder{}).Run(
		t.Context(),
		ai.ToolCall{
			ID:    "call-1",
			Name:  Name,
			Input: json.RawMessage("{"),
		},
	)

	require.NoError(t, err)
	assert.True(t, res.IsError)
	text := resultText(t, res)
	assert.Contains(t, text, "invalid input")
	assert.Contains(t, text, "Expected shape")
}

func TestNew_PanicsOnANilResponder(t *testing.T) {
	assert.Panics(t, func() { New(nil) })
}

func TestRun_ReturnsAResponderError(t *testing.T) {
	responder := &fakeResponder{err: errors.New("surface closed")}

	res, text := call(t, New(responder), validInput())

	assert.True(t, res.IsError)
	assert.Contains(t, text, "surface closed")
}

func TestRun_StopsWhenTheContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	tc := toolCall(t, validInput())

	done := make(chan error, 1)
	go func() {
		_, err := New(blockingResponder{}).Run(ctx, tc)
		done <- err
	}()

	cancel()

	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(runTimeout):
		t.Fatal("Run did not return after the context was canceled")
	}
}

// answeredCall returns the call of in with answers in its input, as an
// approver adds them.
func answeredCall(t *testing.T, in Input, answers map[string]string) ai.ToolCall {
	t.Helper()

	c := toolCall(t, in)
	raw, err := WithAnswers(c.Input, answers)
	require.NoError(t, err)
	c.Input = raw

	return c
}

func TestViaApprover_ReturnsTheAnswersOfTheInput(t *testing.T) {
	in := validInput()
	c := answeredCall(t, in, map[string]string{
		"Which library should we use?": "dayjs",
	})

	res, err := ViaApprover().Run(t.Context(), c)
	require.NoError(t, err)

	assert.False(t, res.IsError)
	var out Output
	require.NoError(t, json.Unmarshal([]byte(resultText(t, res)), &out))
	assert.Equal(t, []Answer{{Header: "Library", Answer: "dayjs"}}, out.Answers)
}

func TestViaApprover_FailsWithNoAnswer(t *testing.T) {
	res, err := ViaApprover().Run(t.Context(), toolCall(t, validInput()))
	require.NoError(t, err)

	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "Which library should we use?")
}

func TestViaApprover_RejectsInvalidQuestions(t *testing.T) {
	in := validInput()
	in.Questions[0].Question = "No question mark"

	res, err := ViaApprover().Run(t.Context(), toolCall(t, in))
	require.NoError(t, err)

	assert.True(t, res.IsError)
}

func TestViaApprover_ReportsABadShapeOfTheAnswersToTheApprover(t *testing.T) {
	c := toolCall(t, validInput())
	fields := map[string]any{}
	require.NoError(t, json.Unmarshal(c.Input, &fields))
	fields["answers"] = []string{"dayjs"}
	raw, err := json.Marshal(fields)
	require.NoError(t, err)
	c.Input = raw

	res, err := ViaApprover().Run(t.Context(), c)
	require.NoError(t, err)

	assert.True(t, res.IsError)
	text := resultText(t, res)
	assert.Contains(t, text, "the approver gave the answers in a bad shape")
	assert.NotContains(t, text, "Expected shape", "the model did not make the mistake")
}

func TestViaApprover_ReadsOnlyTheAnswersKey(t *testing.T) {
	c := toolCall(t, validInput())
	fields := map[string]any{}
	require.NoError(t, json.Unmarshal(c.Input, &fields))
	fields["Answers"] = map[string]string{"Which library should we use?": "dayjs"}
	raw, err := json.Marshal(fields)
	require.NoError(t, err)
	c.Input = raw

	res, err := ViaApprover().Run(t.Context(), c)
	require.NoError(t, err)

	assert.True(t, res.IsError, "a key in another case is not the answers")
}

func TestViaApprover_HasTheNameOfTheTool(t *testing.T) {
	assert.Equal(t, Name, ViaApprover().Info().Name)
}

func TestWithAnswersReplacesTheAnswersOfTheInput(t *testing.T) {
	input := json.RawMessage(`{"questions":[],"answers":{"Which?":"mine"}}`)

	got, err := WithAnswers(input, map[string]string{"Which?": "yours"})
	require.NoError(t, err)

	assert.JSONEq(t, `{"questions":[],"answers":{"Which?":"yours"}}`, string(got))
}

func TestWithAnswersFailsOnAnInputThatIsNotAnObject(t *testing.T) {
	_, err := WithAnswers(json.RawMessage(`[1]`), map[string]string{"Which?": "yours"})

	assert.Error(t, err)
}

func TestWithoutAnswersRemovesTheAnswersOfTheInput(t *testing.T) {
	input := json.RawMessage(`{"questions":[],"answers":{"Which?":"mine"}}`)

	got, err := WithoutAnswers(input)
	require.NoError(t, err)

	assert.JSONEq(t, `{"questions":[]}`, string(got))
}
