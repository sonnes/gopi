package agent

import (
	"context"
	"fmt"
	"slices"

	"github.com/sonnes/gopi/internal/transcript"
	"github.com/sonnes/gopi/pkg/ai"
)

// systemChange returns the message that changes the sections and the
// tools of msgs to the ones of the agent, or nil if they are the same.
// The section blocks come first, then the tool blocks.
func (a *Agent) systemChange(ctx context.Context, msgs []ai.Message) (*ai.Message, error) {
	var sectionBlocks []ai.Content
	if a.cfg.system != nil {
		want, err := a.cfg.system(ctx)
		if err != nil {
			return nil, fmt.Errorf("agent: build the system prompt: %w", err)
		}
		sectionBlocks = sectionChange(currentSections(msgs), want)
	}
	toolBlocks := a.toolChange(msgs)

	if len(sectionBlocks) == 0 && len(toolBlocks) == 0 {
		return nil, nil
	}
	msg := ai.SystemMessage(append(sectionBlocks, toolBlocks...)...)
	return &msg, nil
}

// sectionChange returns the blocks that change shown to want: an
// [ai.SectionRemoved] for each section that is gone, then an
// [ai.Section] for each section that is new or changed. A patch keeps
// the order of shown and appends new names. If that order is not the
// order of want, every shown section is removed and every wanted
// section is set again. It returns nil if shown is want.
func sectionChange(shown, want []ai.Section) []ai.Content {
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
	return blocks
}

// currentSections returns the sections of the system prompt after
// messages: each [ai.Section] of a system message sets its name in
// place or adds it at the end, and each [ai.SectionRemoved] removes its
// name. The fold lives in the agent, because only the agent compares
// the sections of a history with the sections that it wants.
func currentSections(messages []ai.Message) []ai.Section {
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

// splitBase returns the base of msgs and the messages after it. The
// base is the first message, if it is a system message. It holds the
// first sections and the first tools of the history. The first change
// of a new history is the base, because the agent adds it before any
// other message. A history that starts with another message has no
// base.
func splitBase(msgs []ai.Message) (base []ai.Message, rest []ai.Message) {
	if len(msgs) > 0 && msgs[0].Role == ai.RoleSystem {
		return msgs[:1], msgs[1:]
	}
	return nil, msgs
}

// baseOptions returns the options that send base: its [ai.Text] and
// [ai.Section] blocks as [ai.SystemPrompt], and its tools as
// [ai.Tools]. The base never changes, so the options are the same on
// every call of a history, and a provider caches them.
func baseOptions(base []ai.Message) ai.Options {
	var prompt []ai.Content
	for _, m := range base {
		for _, c := range m.Content {
			switch c.(type) {
			case ai.Text, ai.Section:
				prompt = append(prompt, c)
			}
		}
	}

	var opts []ai.Options
	if len(prompt) > 0 {
		opts = append(opts, ai.SystemPrompt(prompt))
	}
	if tools := transcript.CurrentTools(nil, base); len(tools) > 0 {
		opts = append(opts, ai.Tools(tools))
	}
	return ai.JoinOptions(opts...)
}
