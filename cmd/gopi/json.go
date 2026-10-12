package main

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
)

// printer shows the events of a run.
type printer interface {
	print(e agent.Event)
}

// jsonLines prints each event as one line of JSON, for a program that
// reads the output of gopi. [agent.UnmarshalEvent] reads a line back.
type jsonLines struct {
	w io.Writer
}

func (p jsonLines) print(e agent.Event) {
	// An event always marshals, and a failed write to stdout has no
	// other place to go.
	_ = json.NewEncoder(p.w).Encode(e)
}

// thinkingLevels lists the values of the -thinking flag.
var thinkingLevels = []ai.ThinkingLevel{
	ai.ThinkingOff,
	ai.ThinkingLow,
	ai.ThinkingMedium,
	ai.ThinkingHigh,
	ai.ThinkingXHigh,
	ai.ThinkingMax,
}

// parseThinking returns the thinking level that s names.
func parseThinking(s string) (ai.ThinkingLevel, error) {
	level := ai.ThinkingLevel(s)
	if !slices.Contains(thinkingLevels, level) {
		return "", fmt.Errorf("unknown thinking level %q: use off, low, medium, high, xhigh, or max", s)
	}
	return level, nil
}
