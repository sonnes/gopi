package claude

import (
	"cmp"
	"context"
	"strings"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
	"github.com/sonnes/gopi/pkg/stream"
)

const (
	// compactCommand is the command of the CLI that compacts.
	compactCommand = "/compact"
	// strategyName names the strategy of the CLI in the log.
	strategyName = "claude-code"
	// compactFailed is the error of a compaction with no error text.
	compactFailed = "the compaction failed"
	// statusCompacting and compactSuccess are values of a status line.
	statusCompacting = "compacting"
	compactSuccess   = "success"
)

// Compact implements [agent.Runner]. It sends /compact with the
// instructions to the CLI. The log keeps the compaction, and not the
// command.
func (a *Agent) Compact(ctx context.Context, instructions string) *agent.Stream {
	return stream.New(func(push func(agent.Event)) ([]ai.Message, error) {
		em, err := a.core.Start(push)
		if err != nil {
			return nil, err
		}
		text := strings.TrimSpace(compactCommand + " " + instructions)
		return a.drive(ctx, em, []session.Entry{session.Text(text)}, nil, true)
	})
}

// compactStatus reads a status line. The start of a compaction and a
// failed compaction send their events. The boundary line stores a
// compaction that worked.
func (r *runner) compactStatus(l *line) {
	switch {
	case l.Status == statusCompacting && !r.compacting:
		r.compacting = true
		r.em.CompactionStart()
	case l.CompactResult != "" && l.CompactResult != compactSuccess:
		r.compacting = false
		r.em.CompactionFailed(
			session.Compaction{Strategy: strategyName},
			cmp.Or(l.CompactError, compactFailed),
		)
	}
}

// compactBoundary keeps the compaction of a boundary line until the next
// line, because the summary can follow it.
func (r *runner) compactBoundary(l *line) {
	if !r.compacting {
		r.em.CompactionStart()
	}
	r.compacting = false
	data := session.Compaction{Strategy: strategyName}
	if m := l.CompactMetadata; m != nil {
		data.Trigger = m.Trigger
		data.Before = m.PreTokens
		data.After = m.PostTokens
	}
	r.boundary = &data
}

// endBoundary stores the compaction that waits for l. It reports summary
// if l is the summary line of the compaction. The caller then skips l.
func (r *runner) endBoundary(ctx context.Context, l *line) (summary bool, err error) {
	data := *r.boundary
	r.boundary = nil
	if l.Type == "user" && l.IsSynthetic && l.Message != nil {
		summary = true
		data.Summary = l.Message.text()
	}
	return summary, r.em.Compaction(ctx, data)
}
