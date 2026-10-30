package compact_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/compact"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
	"github.com/sonnes/gopi/pkg/tool"
)

// reply is one scripted answer of the provider.
type reply struct {
	text string
	err  error
}

// provider answers each call with the next reply, and records the calls.
type provider struct {
	replies []reply
	calls   [][]ai.Message
	opts    []ai.Options
}

func (p *provider) StreamText(
	_ context.Context,
	_ ai.Model,
	msgs []ai.Message,
	opts ai.Options,
) *ai.EventStream {
	p.calls = append(p.calls, msgs)
	p.opts = append(p.opts, opts)
	if len(p.replies) == 0 {
		return ai.ErrStream(errors.New("provider: no reply left"))
	}
	next := p.replies[0]
	p.replies = p.replies[1:]
	if next.err != nil {
		return ai.ErrStream(next.err)
	}
	return ai.NewEventStream(func(push func(ai.Event)) (*ai.Message, error) {
		m := ai.AssistantMessage(ai.Text{Text: next.text})
		m.StopReason = ai.StopReasonStop
		m.Usage = ai.Usage{Input: 10, Output: 5}
		return &m, nil
	})
}

func lm(p *provider, window int) ai.LanguageModel {
	return ai.LanguageModel{
		Model:    ai.Model{ID: "m", ContextWindow: window},
		Provider: p,
	}
}

// words returns text of about n tokens.
func words(n int) string { return strings.Repeat("abcd", n) }

func msg(id string, m ai.Message) session.Entry {
	return session.Entry{ID: id, Message: m}
}

func call(id, name string) ai.Message {
	return ai.AssistantMessage(ai.ToolCall{
		ID:    id,
		Name:  name,
		Input: json.RawMessage(`{"path":"a.go"}`),
	})
}

func result(id string, tokens int) ai.Message {
	return ai.ToolResultMessage(id, ai.Text{Text: words(tokens)})
}

// sampleEntries is a base, a request, two tool calls, a system change, a
// second request, one more tool call, and an answer.
func sampleEntries() []session.Entry {
	return []session.Entry{
		msg("base", ai.SystemMessage(ai.Text{Text: "prompt"})),
		msg("e1", ai.UserMessage("fix the test")),
		msg("e2", call("c1", tool.ReadFile)),
		msg("e3", result("c1", 40_000)),
		msg("e4", call("c2", "bash")),
		msg("e5", result("c2", 110_000)),
		msg("e6", ai.SystemMessage(ai.ToolRemoved{Name: "x"})),
		msg("e7", ai.UserMessage("keep the old API")),
		msg("e8", call("c3", "bash")),
		msg("e9", result("c3", 8_000)),
		msg("e10", ai.AssistantMessage(ai.Text{Text: "the tests pass"})),
	}
}

func input(p *provider, entries []session.Entry) agent.CompactInput {
	return agent.CompactInput{
		Entries: entries,
		Tokens:  170_000,
		Limit:   160_000,
		Model:   lm(p, 200_000),
	}
}

func texts(msgs []ai.Message) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m.Text())
	}
	return out
}

func TestLimit(t *testing.T) {
	tests := []struct {
		name   string
		window int
		opts   []compact.Option
		want   int
	}{
		{
			name:   "the default is 200k with a large window",
			window: 1_000_000,
			want:   200_000,
		},
		{
			name:   "the default is 80 percent of a small window",
			window: 100_000,
			want:   80_000,
		},
		{
			name: "the default is 200k with no window",
			want: 200_000,
		},
		{
			name:   "tokens and a fraction give the lower one",
			window: 200_000,
			opts:   []compact.Option{compact.Limit(150_000, 0.7)},
			want:   140_000,
		},
		{
			name:   "only tokens",
			window: 200_000,
			opts:   []compact.Option{compact.Limit(50_000, 0)},
			want:   50_000,
		},
		{
			name:   "only a fraction",
			window: 200_000,
			opts:   []compact.Option{compact.Limit(0, 0.5)},
			want:   100_000,
		},
		{
			name: "a fraction with no window keeps the default",
			opts: []compact.Option{compact.Limit(0, 0.5)},
			want: 200_000,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := ai.Model{ContextWindow: tt.window}
			assert.Equal(t, tt.want, compact.Summarize(tt.opts...).Limit(m))
			assert.Equal(t, tt.want, compact.Summarize(tt.opts...).Limit(m))
		})
	}
}

func TestSummarizeReplacesTheOldPart(t *testing.T) {
	p := &provider{replies: []reply{{text: "the summary"}}}
	s := compact.Summarize(compact.KeepRecent(5_000), compact.KeepFiles(0))

	in := input(p, sampleEntries())
	in.Options = ai.Thinking(ai.ThinkingHigh)
	res, err := s.Compact(context.Background(), in)
	require.NoError(t, err)

	require.Len(t, res.Edits, 1)
	assert.Equal(t, "e2", res.Edits[0].First, "the first request stays")
	assert.Equal(t, "e5", res.Edits[0].Last, "the cut is before the second request, and the system message stays")
	require.Len(t, res.Edits[0].Messages, 1)
	assert.Contains(t, res.Edits[0].Messages[0].Text(), "the summary")
	assert.Equal(t, ai.RoleUser, res.Edits[0].Messages[0].Role)
	assert.Equal(t, "summarize", res.Strategy)
	assert.Equal(t, 10, res.Usage.Input)
	require.NoError(t, session.CheckEdits(in.Entries, res.Edits))

	require.Len(t, p.calls, 1)
	sent := p.calls[0]
	assert.Equal(t, "fix the test", sent[0].Text(), "the call has the prefix, without the base")
	assert.Len(t, sent, len(in.Entries), "the view after the base, and the prompt")
	assert.Equal(t, ai.RoleUser, sent[len(sent)-1].Role)
	level, ok := ai.GetOption(p.opts[0], ai.Thinking)
	assert.True(t, ok, "the call has the options of the agent")
	assert.Equal(t, ai.ThinkingHigh, level)
}

func TestSummarizePromptHasTheRules(t *testing.T) {
	p := &provider{replies: []reply{{text: "s"}}}
	in := input(p, sampleEntries())
	in.Instructions = "keep the schema decisions"
	_, err := compact.Summarize().Compact(context.Background(), in)
	require.NoError(t, err)

	sent := p.calls[0]
	prompt := sent[len(sent)-1].Text()
	for _, want := range []string{
		"each user message",
		"errors and their fixes",
		"security constraint",
		"Only a user turn",
		"copy the user messages of the old summary",
		"This request is not a user message",
		"latest user message before this request",
		"keep the schema decisions",
	} {
		assert.Contains(t, prompt, want)
	}
}

func TestSummarizeCut(t *testing.T) {
	oneTurn := []session.Entry{
		msg("e1", ai.UserMessage("do it")),
		msg("e2", call("c1", "bash")),
		msg("e3", result("c1", 50_000)),
		msg("e4", call("c2", "bash")),
		msg("e5", result("c2", 50_000)),
		msg("e6", call("c3", "bash")),
		msg("e7", result("c3", 30_000)),
	}
	tests := []struct {
		name      string
		entries   []session.Entry
		opts      []compact.Option
		wantFirst string
		wantLast  string
	}{
		{
			name:      "one long turn cuts at an assistant message",
			entries:   oneTurn,
			opts:      []compact.Option{compact.KeepRecent(20_000), compact.KeepFiles(0)},
			wantFirst: "e2",
			wantLast:  "e5",
		},
		{
			name:      "KeepRecent 0 replaces the whole tail",
			entries:   oneTurn,
			opts:      []compact.Option{compact.KeepRecent(0), compact.KeepFiles(0)},
			wantFirst: "e2",
			wantLast:  "e7",
		},
		{
			name:      "KeepFirst 0 replaces the first request",
			entries:   oneTurn,
			opts:      []compact.Option{compact.KeepRecent(20_000), compact.KeepFirst(0), compact.KeepFiles(0)},
			wantFirst: "e1",
			wantLast:  "e5",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &provider{replies: []reply{{text: "s"}}}
			res, err := compact.Summarize(tt.opts...).Compact(
				context.Background(),
				input(p, tt.entries),
			)
			require.NoError(t, err)
			require.NotEmpty(t, res.Edits)
			assert.Equal(t, tt.wantFirst, res.Edits[0].First)
			assert.Equal(t, tt.wantLast, res.Edits[0].Last)
			require.NoError(t, session.CheckEdits(tt.entries, res.Edits))
		})
	}
}

func TestSummarizeSkipsAnEphemeralBoundary(t *testing.T) {
	entries := []session.Entry{
		msg("e1", ai.UserMessage("first")),
		{ID: "note", Ephemeral: true, Message: ai.UserMessage("now")},
		msg("e2", ai.AssistantMessage(ai.Text{Text: words(10_000)})),
		{ID: "note2", Ephemeral: true, Message: ai.UserMessage("now")},
		msg("e3", ai.UserMessage(words(30_000))),
		msg("e4", ai.AssistantMessage(ai.Text{Text: "end"})),
	}
	p := &provider{replies: []reply{{text: "s"}}}
	res, err := compact.Summarize(compact.KeepRecent(20_000)).Compact(
		context.Background(),
		input(p, entries),
	)
	require.NoError(t, err)
	require.Len(t, res.Edits, 1)
	assert.Equal(t, "e2", res.Edits[0].First)
	assert.Equal(t, "e2", res.Edits[0].Last)
	require.NoError(t, session.CheckEdits(entries, res.Edits))
}

func TestSummarizeWithNothingToReplace(t *testing.T) {
	entries := []session.Entry{
		msg("e1", ai.UserMessage("first")),
		msg("e2", ai.AssistantMessage(ai.Text{Text: "ok"})),
	}
	p := &provider{}
	res, err := compact.Summarize().Compact(context.Background(), input(p, entries))
	require.NoError(t, err)
	assert.Empty(t, res.Edits)
	assert.Empty(t, p.calls, "no edits, so no model call")
}

func TestSummarizeReplacesAnOldSummary(t *testing.T) {
	entries := []session.Entry{
		msg("e1", ai.UserMessage("first")),
		msg("e2.0", ai.UserMessage("old summary")),
		msg("e20", ai.AssistantMessage(ai.Text{Text: words(50_000)})),
		msg("e21", ai.UserMessage("next")),
		msg("e22", ai.AssistantMessage(ai.Text{Text: words(30_000)})),
	}
	p := &provider{replies: []reply{{text: "new"}}}
	res, err := compact.Summarize().Compact(context.Background(), input(p, entries))
	require.NoError(t, err)
	require.Len(t, res.Edits, 1)
	assert.Equal(t, "e2.0", res.Edits[0].First)
	assert.Equal(t, "e20", res.Edits[0].Last)
}

func TestKeepFiles(t *testing.T) {
	var entries []session.Entry
	entries = append(entries, msg("u", ai.UserMessage("go")))
	for i := range 4 {
		id := fmt.Sprintf("r%d", i)
		tokens := 1_000
		if i == 3 {
			tokens = 9_000
		}
		entries = append(entries,
			msg("a"+id, call(id, tool.ReadFile)),
			msg("b"+id, result(id, tokens)),
		)
	}
	failed := result("bad", 10)
	failed.IsError = true
	entries = append(entries,
		msg("abad", call("bad", tool.ReadFile)),
		msg("bbad", failed),
		msg("c", call("other", "bash")),
		msg("d", result("other", 10)),
		msg("u2", ai.UserMessage("more")),
		msg("z", ai.AssistantMessage(ai.Text{Text: words(30_000)})),
	)

	p := &provider{replies: []reply{{text: "s"}}}
	res, err := compact.Summarize(compact.KeepFiles(2)).Compact(
		context.Background(),
		input(p, entries),
	)
	require.NoError(t, err)
	require.Len(t, res.Edits, 1)
	require.NoError(t, session.CheckEdits(entries, res.Edits))

	got := res.Edits[0].Messages
	require.Len(t, got, 5, "the summary, then two calls and their results")
	assert.Equal(t, "r2", got[1].ToolCalls()[0].ID)
	assert.Equal(t, "r2", got[2].ToolCallID)
	assert.Equal(t, "r3", got[3].ToolCalls()[0].ID)
	assert.Equal(t, "r3", got[4].ToolCallID)
	assert.Equal(t, 1_000, ai.EstimateTokens(got[2]), "a small result stays")
	assert.Less(t, ai.EstimateTokens(got[4]), 5_100, "a large result is clipped")
}

func TestSummarizeClipsALargeTail(t *testing.T) {
	failed := result("c2", 90_000)
	failed.IsError = true
	entries := []session.Entry{
		msg("e1", ai.UserMessage("go")),
		msg("e2", ai.AssistantMessage(ai.Text{Text: "old"})),
		msg("e3", ai.UserMessage("more")),
		msg("e4", ai.AssistantMessage(
			ai.ToolCall{ID: "c1", Name: "bash", Input: json.RawMessage(`{}`)},
			ai.ToolCall{ID: "c2", Name: "bash", Input: json.RawMessage(`{}`)},
		)),
		msg("e5", result("c1", 300_000)),
		msg("e6", failed),
	}
	p := &provider{replies: []reply{{text: "s"}}}
	res, err := compact.Summarize().Compact(context.Background(), input(p, entries))
	require.NoError(t, err)

	require.Len(t, res.Edits, 3)
	assert.Equal(t, "e2", res.Edits[0].First)
	assert.Equal(t, "e5", res.Edits[1].First)
	assert.Equal(t, "e6", res.Edits[2].First)
	clipped := res.Edits[2].Messages[0]
	assert.True(t, clipped.IsError, "a clipped error stays an error")
	assert.Equal(t, "c2", clipped.ToolCallID)
	assert.Less(t, ai.EstimateTokens(clipped), 5_100)
	require.NoError(t, session.CheckEdits(entries, res.Edits))
}

func TestCustomModelReadsSerializedText(t *testing.T) {
	agentModel := &provider{}
	small := &provider{replies: []reply{{text: "s"}}}
	s := compact.Summarize(
		compact.KeepRecent(5_000),
		compact.KeepFiles(0),
		compact.Model(lm(small, 100_000)),
	)
	res, err := s.Compact(context.Background(), input(agentModel, sampleEntries()))
	require.NoError(t, err)
	require.Len(t, res.Edits, 1)

	assert.Empty(t, agentModel.calls)
	require.Len(t, small.calls, 1)
	require.Len(t, small.calls[0], 1, "one message with the history as text")
	sent := small.calls[0][0].Text()
	assert.Contains(t, sent, "fix the test")
	assert.Contains(t, sent, "[e8]")
	assert.Less(t, ai.EstimateTokens(small.calls[0][0]), 100_000, "the tool results are clipped")
}

func TestCustomModelFailsIfTheTextDoesNotFit(t *testing.T) {
	small := &provider{}
	s := compact.Summarize(
		compact.KeepRecent(0),
		compact.Model(lm(small, 20_000)),
	)
	entries := []session.Entry{
		msg("e1", ai.UserMessage("go")),
		msg("e2", ai.UserMessage(words(90_000))),
		msg("e3", ai.AssistantMessage(ai.Text{Text: "ok"})),
	}
	_, err := s.Compact(context.Background(), input(&provider{}, entries))
	assert.ErrorIs(t, err, compact.ErrTooLarge)
	assert.Empty(t, small.calls)
}

func TestOverflowOfTheSideCallReadsSerializedText(t *testing.T) {
	p := &provider{replies: []reply{
		{err: fmt.Errorf("provider: %w", ai.ErrContextExceeded)},
		{text: "s"},
	}}
	s := compact.Summarize(compact.KeepRecent(5_000), compact.KeepFiles(0))
	res, err := s.Compact(context.Background(), input(p, sampleEntries()))
	require.NoError(t, err)
	require.Len(t, res.Edits, 1)
	require.Len(t, p.calls, 2)
	assert.Len(t, p.calls[1], 1, "the second call reads text")
}

func TestSummarizeFails(t *testing.T) {
	p := &provider{replies: []reply{{err: errors.New("boom")}}}
	_, err := compact.Summarize().Compact(context.Background(), input(p, sampleEntries()))
	assert.ErrorContains(t, err, "boom")

	p = &provider{replies: []reply{{text: ""}}}
	_, err = compact.Summarize().Compact(context.Background(), input(p, sampleEntries()))
	assert.ErrorContains(t, err, "no text")
}

func TestSummarizeInAnAgent(t *testing.T) {
	big := ai.AssistantMessage(ai.Text{Text: words(100)})
	big.StopReason = ai.StopReasonStop
	big.Usage = ai.Usage{Input: 1000}
	p := &scripted{messages: []ai.Message{big}, provider: provider{replies: []reply{{text: "the summary"}, {text: "again"}}}}
	a := agent.New(
		ai.LanguageModel{Provider: p},
		agent.Compaction(compact.Summarize(compact.Limit(500, 0), compact.KeepRecent(0))),
	)
	ctx := context.Background()
	s := a.Run(ctx, session.Text("first"))
	_, err := s.Wait()
	require.NoError(t, err)

	s = a.Run(ctx, session.Text("second"))
	_, err = s.Wait()
	require.NoError(t, err)

	got := texts(a.State().View())
	require.Len(t, got, 3)
	assert.Equal(t, "first", got[0])
	assert.Contains(t, got[1], "the summary")
	assert.Equal(t, "again", got[2])
	assert.Len(t, a.State().Messages(), 4, "the state keeps the history")
}

// scripted answers the first calls with fixed messages, then as provider.
type scripted struct {
	provider
	messages []ai.Message
}

func (s *scripted) StreamText(
	ctx context.Context,
	m ai.Model,
	msgs []ai.Message,
	opts ai.Options,
) *ai.EventStream {
	if len(s.messages) == 0 {
		return s.provider.StreamText(ctx, m, msgs, opts)
	}
	next := s.messages[0]
	s.messages = s.messages[1:]
	return ai.NewEventStream(func(func(ai.Event)) (*ai.Message, error) {
		return &next, nil
	})
}
