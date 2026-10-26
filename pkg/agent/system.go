package agent

import (
	"context"
	"fmt"

	"github.com/sonnes/gopi/internal/transcript"
	"github.com/sonnes/gopi/pkg/agent/internal/sections"
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
		sectionBlocks, err = sections.Change(sections.Current(msgs), want)
		if err != nil {
			return nil, fmt.Errorf("agent: build the system prompt: %w", err)
		}
	}
	toolBlocks := a.toolChange(msgs)

	if len(sectionBlocks) == 0 && len(toolBlocks) == 0 {
		return nil, nil
	}
	msg := ai.SystemMessage(append(sectionBlocks, toolBlocks...)...)
	return &msg, nil
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
