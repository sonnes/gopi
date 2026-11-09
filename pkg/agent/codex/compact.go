package codex

import (
	"context"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/stream"
)

// strategyName names the strategy of the CLI in the log.
const strategyName = "codex"

// Compact implements [agent.Runner]. It ignores the instructions,
// because thread/compact/start takes only the thread.
func (a *Agent) Compact(ctx context.Context, _ string) *agent.Stream {
	return stream.New(func(push func(agent.Event)) ([]ai.Message, error) {
		e, err := a.core.Start(push)
		if err != nil {
			return nil, err
		}
		c, threadID, err := a.connect(ctx)
		if err != nil {
			return e.Finish(ctx, ai.Usage{}, err)
		}
		t := a.newTurn(c, e, threadID)
		c.bindHooks(ctx)
		usage, err := t.compact(ctx)
		return e.FinishByModel(ctx, map[string]ai.Usage{t.model: usage}, a.endTurns(ctx, c, t, err))
	})
}
