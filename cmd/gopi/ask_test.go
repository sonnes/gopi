package main

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/tool/askquestion"
)

func TestTerminal_Ask(t *testing.T) {
	color := askquestion.Question{
		Header:   "Color",
		Question: "Which color?",
		Options: []askquestion.Option{
			{Label: "Red", Description: "warm"},
			{Label: "Blue"},
			{Label: "Green"},
		},
	}
	sizes := askquestion.Question{
		Header:      "Sizes",
		Question:    "Which sizes?",
		MultiSelect: true,
		Options: []askquestion.Option{
			{Label: "S"},
			{Label: "M"},
			{Label: "L"},
		},
	}

	tests := []struct {
		name      string
		questions []askquestion.Question
		lines     []string
		want      []askquestion.Answer
		wantOut   string
	}{
		{
			name:      "a number picks an option",
			questions: []askquestion.Question{color},
			lines:     []string{"2"},
			want:      []askquestion.Answer{{Header: "Color", Answer: "Blue"}},
			wantOut: `
? Color: Which color?
  1. Red  warm
  2. Blue
  3. Green
answer: `,
		},
		{
			name:      "text that is not a number is the answer",
			questions: []askquestion.Question{color},
			lines:     []string{"  purple  "},
			want:      []askquestion.Answer{{Header: "Color", Answer: "purple"}},
		},
		{
			name:      "a multi-select question takes numbers with commas",
			questions: []askquestion.Question{sizes},
			lines:     []string{"1, 3"},
			want:      []askquestion.Answer{{Header: "Sizes", Answer: "S, L"}},
			wantOut: `
? Sizes: Which sizes?
  1. S
  2. M
  3. L
answers, with commas: `,
		},
		{
			name:      "each question reads its own line",
			questions: []askquestion.Question{color, sizes},
			lines:     []string{"1", "2"},
			want: []askquestion.Answer{
				{Header: "Color", Answer: "Red"},
				{Header: "Sizes", Answer: "M"},
			},
		},
		{
			name:      "a number out of range is text",
			questions: []askquestion.Question{color},
			lines:     []string{"9"},
			want:      []askquestion.Answer{{Header: "Color", Answer: "9"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out strings.Builder
			r := terminal{
				w:  &out,
				in: readInput(strings.NewReader(strings.Join(tt.lines, "\n")+"\n"), false),
			}

			got, err := r.Ask(t.Context(), "call_1", tt.questions)

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			if tt.wantOut != "" {
				assert.Equal(t, strings.TrimPrefix(tt.wantOut, "\n"), out.String())
			}
		})
	}
}

func TestTerminal_Ask_Stops(t *testing.T) {
	question := []askquestion.Question{{
		Header:   "Color",
		Question: "Which color?",
		Options:  []askquestion.Option{{Label: "Red"}, {Label: "Blue"}},
	}}

	t.Run("a canceled context leaves the line for the prompt loop", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		in := readInput(strings.NewReader("next prompt\n"), false)
		r := terminal{
			w:  &strings.Builder{},
			in: in,
		}

		_, err := r.Ask(ctx, "call_1", question)

		require.ErrorIs(t, err, context.Canceled)
		line, ok := in.next()
		require.True(t, ok)
		assert.Equal(t, "next prompt", line)
	})

	t.Run("the end of the input", func(t *testing.T) {
		r := terminal{
			w:  &strings.Builder{},
			in: readInput(strings.NewReader(""), false),
		}

		_, err := r.Ask(t.Context(), "call_1", question)

		require.ErrorContains(t, err, "no answer")
	})
}

// signalWriter closes asked when a write ends with the prompt of an
// answer.
type signalWriter struct {
	strings.Builder
	asked chan struct{}
}

func (w *signalWriter) Write(p []byte) (int, error) {
	n, err := w.Builder.Write(p)
	if strings.HasSuffix(string(p), "answer: ") {
		close(w.asked)
	}
	return n, err
}

func TestTerminal_Ask_OnATerminal(t *testing.T) {
	question := []askquestion.Question{{
		Header:   "Color",
		Question: "Which color?",
		Options:  []askquestion.Option{{Label: "Red"}, {Label: "Blue"}},
	}}
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })
	in := readInput(pr, true)
	shown := &shownCalls{}
	w := &signalWriter{asked: make(chan struct{})}
	r := terminal{
		w:     w,
		in:    in,
		shown: shown,
	}

	// A line typed during the run, before the question.
	_, err := io.WriteString(pw, "next prompt\n")
	require.NoError(t, err)

	type result struct {
		answers []askquestion.Answer
		err     error
	}
	done := make(chan result, 1)
	go func() {
		answers, err := r.Ask(t.Context(), "call_1", question)
		done <- result{answers, err}
	}()

	// The question waits until the tree prints its call.
	time.Sleep(50 * time.Millisecond)
	assert.Empty(t, w.String())
	shown.mark("call_1")
	<-w.asked

	_, err = io.WriteString(pw, "2\n")
	require.NoError(t, err)
	got := <-done

	require.NoError(t, got.err)
	assert.Equal(t, []askquestion.Answer{{Header: "Color", Answer: "Blue"}}, got.answers)
	line, ok := in.next()
	require.True(t, ok)
	assert.Equal(t, "next prompt", line)
}

func TestTerminal_Approve(t *testing.T) {
	tests := []struct {
		line string
		want agent.Decision
	}{
		{"y", agent.Decision{Allow: true}},
		{" YES ", agent.Decision{Allow: true}},
		{"a", agent.Decision{Allow: true, Always: true}},
		{"always", agent.Decision{Allow: true, Always: true}},
		{"n", agent.Decision{}},
		{"", agent.Decision{}},
		{"maybe", agent.Decision{}},
	}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			var out strings.Builder
			r := terminal{
				w:  &out,
				in: readInput(strings.NewReader(tt.line+"\n"), false),
			}
			call := ai.ToolCall{ID: "call_1", Name: "bash", Input: json.RawMessage(`{"command":"rm -rf .bin"}`)}

			got, err := r.Approve(t.Context(), agent.ApprovalRequest{Call: call})

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, `? allow bash {"command":"rm -rf .bin"}? [y]es, [a]lways, [n]o: `, out.String())
		})
	}
}
