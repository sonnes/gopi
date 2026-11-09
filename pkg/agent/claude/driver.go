package claude

import (
	"context"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
)

// Driver returns an [agent.Driver] that starts a Claude Code agent for
// each session. Its Start calls [Durable] with the config of the
// session and opts.
func Driver(opts ...Option) agent.Driver {
	return driver{opts: opts}
}

type driver struct {
	opts []Option
}

// ListModels starts the CLI with the options of the driver, and returns
// the models that the initialize response lists: each alias that
// --model takes, such as "opus", then each full ID that an alias
// resolves to. The CLI makes no model call, but it starts as for a run,
// so the hooks of its settings run, such as a SessionStart hook. A
// catalog calls it from FetchDriverModels.
func (d driver) ListModels(ctx context.Context) ([]ai.Model, error) {
	return listModels(ctx, newOptions(d.opts))
}

// Start starts the durable agent of the session c.SessionID.
func (d driver) Start(ctx context.Context, c agent.Config) (agent.Runner, error) {
	a, err := Durable(ctx, c, d.opts...)
	if err != nil {
		return nil, err
	}
	return a, nil
}
