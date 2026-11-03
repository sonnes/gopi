package codex

import (
	"context"
	"fmt"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/internal/emit"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/stream"
)

// strategyName names the strategy of the CLI in the log.
const strategyName = "codex"

var _ agent.ManualCompactor = (*Agent)(nil)

// Compact implements [agent.ManualCompactor]. It ignores the instructions,
// because thread/compact/start takes only the thread.
func (a *Agent) Compact(ctx context.Context, _ string) *agent.Stream {
	return stream.New(func(push func(agent.Event)) ([]ai.Message, error) {
		e := emit.New(a.log, a.cfg.name, a.state, push)
		if err := e.Start(); err != nil {
			return nil, fmt.Errorf("codex: %w", err)
		}
		c, threadID, err := a.connect(ctx)
		if err != nil {
			return e.Finish(ctx, ai.Usage{}, err)
		}
		t := a.newTurn(c, e, threadID)
		// The error of a hook belongs to the run that called it.
		_ = c.hookErr()
		c.bindHooks(ctx)
		usage, err := t.compact(ctx)
		return e.FinishByModel(ctx, map[string]ai.Usage{t.model: usage}, a.endTurns(ctx, c, t, err))
	})
}
