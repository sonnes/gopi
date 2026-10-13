// Package partial builds an assistant message from model events as the
// events arrive. The agent loop and the CLI agents use it, so that a
// message_update carries the same snapshot in both.
package partial

import "github.com/sonnes/gopi/pkg/ai"

// Accumulate applies one model event to the partial message. It grows
// the content blocks as the events arrive.
func Accumulate(m *ai.Message, evt *ai.Event) {
	i := evt.ContentIndex
	switch evt.Type {
	case ai.EventTextStart:
		grow(m, i, ai.Text{})
	case ai.EventTextDelta:
		grow(m, i, ai.Text{})
		if t, ok := m.Content[i].(ai.Text); ok {
			t.Text += evt.Delta
			m.Content[i] = t
		}
	case ai.EventTextEnd:
		grow(m, i, ai.Text{})
		if t, ok := m.Content[i].(ai.Text); ok {
			t.Text = evt.Content
			m.Content[i] = t
		}
	case ai.EventThinkStart:
		grow(m, i, ai.ThinkingBlock{})
	case ai.EventThinkDelta:
		grow(m, i, ai.ThinkingBlock{})
		if t, ok := m.Content[i].(ai.ThinkingBlock); ok {
			t.Thinking += evt.Delta
			m.Content[i] = t
		}
	case ai.EventThinkEnd:
		grow(m, i, ai.ThinkingBlock{})
		if t, ok := m.Content[i].(ai.ThinkingBlock); ok {
			t.Thinking = evt.Content
			m.Content[i] = t
		}
	case ai.EventToolStart:
		grow(m, i, ai.ToolCall{})
	case ai.EventToolEnd:
		if evt.ToolCall != nil {
			grow(m, i, ai.ToolCall{})
			m.Content[i] = *evt.ToolCall
		}
	}
}

// grow extends the content of m until index i exists.
func grow(m *ai.Message, i int, zero ai.Content) {
	for len(m.Content) <= i {
		m.Content = append(m.Content, zero)
	}
}

// Snapshot returns a copy of m with its own content slice, so later
// deltas do not change an event that was already sent.
func Snapshot(m *ai.Message) *ai.Message {
	cp := *m
	cp.Content = make([]ai.Content, len(m.Content))
	copy(cp.Content, m.Content)
	return &cp
}
