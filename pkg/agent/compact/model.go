package compact

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/internal/sections"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
)

// ErrTooLarge reports that the serialized history does not fit the
// context window of the model, even with each tool result clipped.
var ErrTooLarge = errors.New("compact: the history does not fit the context window")

const (
	// charsPerToken is the characters of text in one clipped token. It is
	// the same ratio as [ai.EstimateTokens].
	charsPerToken = 4
	// outputReserve is the part of a context window that a serialized
	// history leaves for the answer.
	outputReserve = 16_384
	// firstClip is the tokens of each tool result in the first try to fit
	// a serialized history. Each next try halves it.
	firstClip = 16_000
	// lastClip is the smallest clip of a tool result.
	lastClip = 250
)

// ask sends prompt to the model and returns its text. With no model of
// [Model], the call has the view as its prefix. If the provider refuses
// the size of that prefix, ask sends the history as text, so a compaction
// after an overflow can work.
func (o options) ask(
	ctx context.Context,
	in agent.CompactInput,
	prompt string,
) (string, ai.Usage, error) {
	if o.model != nil {
		return askText(ctx, *o.model, in.Entries, prompt)
	}

	msgs := make([]ai.Message, 0, len(in.Entries)+1)
	for _, e := range in.Entries {
		msgs = append(msgs, e.Message)
	}
	_, rest := sections.Base(msgs)
	request := append(slices.Clip(rest), ai.UserMessage(prompt))

	text, usage, err := complete(ctx, in.Model, request, in.Options)
	if errors.Is(err, ai.ErrContextExceeded) {
		return askText(ctx, in.Model, in.Entries, prompt)
	}
	return text, usage, err
}

// askText sends the history as serialized text, and reads no cache.
func askText(
	ctx context.Context,
	lm ai.LanguageModel,
	entries []session.Entry,
	prompt string,
) (string, ai.Usage, error) {
	history, err := fit(lm.Model, entries, prompt)
	if err != nil {
		return "", ai.Usage{}, err
	}
	request := []ai.Message{
		ai.UserMessage("<history>\n" + history + "\n</history>\n\n" + prompt),
	}
	return complete(ctx, lm, request, nil)
}

// complete makes one model call and returns its text. A call that returns
// tool calls and no text fails, because the strategy gives no results.
func complete(
	ctx context.Context,
	lm ai.LanguageModel,
	msgs []ai.Message,
	opts ai.Options,
) (string, ai.Usage, error) {
	m, err := lm.StreamText(ctx, msgs, opts).Wait()
	if err != nil {
		return "", ai.Usage{}, err
	}
	if m == nil {
		return "", ai.Usage{}, errors.New("compact: the provider returned no message")
	}
	text := strings.TrimSpace(m.Text())
	if text == "" {
		return "", m.Usage, fmt.Errorf(
			"compact: the model returned no text, stop reason %q: %s",
			m.StopReason,
			m.StopDetail,
		)
	}
	if m.StopReason == ai.StopReasonLength {
		return "", m.Usage, errors.New("compact: the output of the model was cut short")
	}
	return text, m.Usage, nil
}

// fit serializes entries so they fit the window of the model, with a
// smaller clip of each tool result until they do. A model with no known
// window gets the first clip.
func fit(m ai.Model, entries []session.Entry, prompt string) (string, error) {
	budget := m.ContextWindow - outputReserve - ai.EstimateTokens(ai.UserMessage(prompt))
	for clip := firstClip; clip >= lastClip; clip /= 2 {
		text := serialize(entries, clip)
		if m.ContextWindow == 0 || ai.EstimateTokens(ai.UserMessage(text)) <= budget {
			return text, nil
		}
	}
	return "", ErrTooLarge
}

// serialize writes each entry as text, with the ID of the entry, and
// clips each tool result to clip tokens. It skips a system message,
// because the sections and the tools do not need a summary.
func serialize(entries []session.Entry, clip int) string {
	var b strings.Builder
	for _, e := range entries {
		m := e.Message
		if m.Role == ai.RoleSystem {
			continue
		}
		text := m.Text()
		if m.Role == ai.RoleToolResult {
			text = clipText(text, clip)
		}
		fmt.Fprintf(&b, "[%s] %s: %s\n", e.ID, m.Role, text)
		for _, tc := range m.ToolCalls() {
			fmt.Fprintf(&b, "[%s] call %s %s\n", e.ID, tc.Name, tc.Input)
		}
	}
	return b.String()
}

// clipMessage returns the tool result m with its text clipped to tokens.
// It keeps the call ID and the error mark. A file in the result becomes
// part of the clip, because the text of a clip is the only content.
func clipMessage(m ai.Message, tokens int) ai.Message {
	clipped := m
	clipped.Content = []ai.Content{ai.Text{Text: clipText(m.Text(), tokens)}}
	return clipped
}

// clipText keeps the start and the end of s, so the model can read the
// head of a file and the tail of a log.
func clipText(s string, tokens int) string {
	limit := tokens * charsPerToken
	if len(s) <= limit {
		return s
	}
	head := strings.ToValidUTF8(s[:limit/2], "")
	tail := strings.ToValidUTF8(s[len(s)-limit/2:], "")
	return fmt.Sprintf("%s\n[%d characters clipped]\n%s", head, len(s)-limit, tail)
}
