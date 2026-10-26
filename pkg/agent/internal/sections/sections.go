// Package sections folds and changes the sections of a system prompt
// in a history. Every driver uses it: the native agent sends a change
// as a system message, Claude Code as a reminder in the next user
// message, and Codex as a developer message before the turn. Each
// driver stores the change in its log, so the log records the sections
// that the model read.
//
// The reasons are in docs/concepts/agent/section.md.
package sections

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/sonnes/gopi/pkg/ai"
)

// sep joins the parts of the text of the base.
const sep = "\n\n"

// Change returns the blocks that change shown to want: an
// [ai.SectionRemoved] for each section that is gone, then an
// [ai.Section] for each section that is new or changed. A patch keeps
// the order of shown and appends new names. If that order is not the
// order of want, every shown section is removed and every wanted
// section is set again. It returns nil if shown is want. Two sections
// of want with one name fail, because the fold keeps one section for
// each name.
func Change(shown, want []ai.Section) ([]ai.Content, error) {
	for i, s := range want {
		if slices.ContainsFunc(want[:i], func(o ai.Section) bool { return o.Name == s.Name }) {
			return nil, fmt.Errorf("two sections are named %q", s.Name)
		}
	}

	named := func(name string) func(ai.Section) bool {
		return func(s ai.Section) bool { return s.Name == name }
	}

	var order []string
	for _, s := range shown {
		if slices.ContainsFunc(want, named(s.Name)) {
			order = append(order, s.Name)
		}
	}
	for _, s := range want {
		if !slices.ContainsFunc(shown, named(s.Name)) {
			order = append(order, s.Name)
		}
	}
	keepsOrder := slices.EqualFunc(order, want, func(name string, s ai.Section) bool {
		return name == s.Name
	})

	var blocks []ai.Content
	for _, s := range shown {
		if !keepsOrder || !slices.ContainsFunc(want, named(s.Name)) {
			blocks = append(blocks, ai.SectionRemoved{Name: s.Name})
		}
	}
	for _, s := range want {
		if !keepsOrder || !slices.Contains(shown, s) {
			blocks = append(blocks, s)
		}
	}
	return blocks, nil
}

// Current returns the sections of the system prompt after messages:
// each [ai.Section] of a system message sets its name in place or adds
// it at the end, and each [ai.SectionRemoved] removes its name. The
// fold lives with the drivers, because only a driver compares the
// sections of a history with the sections that it wants.
func Current(messages []ai.Message) []ai.Section {
	var shown []ai.Section
	for _, m := range messages {
		if m.Role != ai.RoleSystem {
			continue
		}
		for _, c := range m.Content {
			switch v := c.(type) {
			case ai.Section:
				i := slices.IndexFunc(shown, func(s ai.Section) bool { return s.Name == v.Name })
				if i >= 0 {
					shown[i] = v
				} else {
					shown = append(shown, v)
				}
			case ai.SectionRemoved:
				shown = slices.DeleteFunc(shown, func(s ai.Section) bool { return s.Name == v.Name })
			}
		}
	}
	return shown
}

// Base returns the base of msgs and the messages after it. The base is
// the first message, if it is a system message. It holds the first
// sections and the first tools of the history. The first change of a
// new history is the base, because a driver adds it before any other
// message. A history that starts with another message has no base.
func Base(msgs []ai.Message) (base []ai.Message, rest []ai.Message) {
	if len(msgs) > 0 && msgs[0].Role == ai.RoleSystem {
		return msgs[:1], msgs[1:]
	}
	return nil, msgs
}

// Text returns the text of the base of msgs: each [ai.Text] and
// [ai.Section] block, joined by a blank line, with no frame. An agent
// CLI gets it as its system prompt at each start. The base never
// changes, so a resumed CLI gets the same prompt, and its cache stays.
// A history with no base has no text.
func Text(msgs []ai.Message) string {
	base, _ := Base(msgs)
	var parts []string
	for _, m := range base {
		for _, c := range m.Content {
			switch v := c.(type) {
			case ai.Text:
				parts = append(parts, v.Text)
			case ai.Section:
				parts = append(parts, v.Text)
			}
		}
	}
	return strings.Join(parts, sep)
}

// ForCLI returns the system message that a CLI agent stores before the
// input of a run: the base of an empty history, or the change from the
// sections of msgs to the sections of fn. It returns nil messages if
// the sections are the same, or if fn is nil.
//
// An empty history has no conversation yet, so the CLI starts in this
// run and gets the base as its system prompt, with [Text]. A CLI takes
// no system message in a conversation, so the driver sends a change in
// its own way.
func ForCLI(
	ctx context.Context,
	fn func(context.Context) ([]ai.Section, error),
	msgs []ai.Message,
) (base, change *ai.Message, err error) {
	if fn == nil {
		return nil, nil, nil
	}
	want, err := fn(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("build the system prompt: %w", err)
	}
	blocks, err := Change(Current(msgs), want)
	if err != nil || len(blocks) == 0 {
		return nil, nil, err
	}

	msg := ai.SystemMessage(blocks...)
	if len(msgs) == 0 {
		return &msg, nil, nil
	}
	return nil, &msg, nil
}
