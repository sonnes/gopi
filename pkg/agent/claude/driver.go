package claude

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/catalog/models"
)

// Models returns the models of Claude Code that a catalog registers with
// the driver: the aliases that --model takes, such as "opus", and the
// full IDs, with the default model first. The list is the "claude-code"
// entry of pkg/catalog/models/models.json. Each call returns a new list. A bare
// model ID names the driver only if no text provider serves the same
// ID. Otherwise give the full spec, such as
// "claude-code/claude-opus-5-5". To list the models of the installed
// CLI, call the ListModels method of the driver.
func Models() []ai.Model {
	return models.Of("claude-code")
}

// Driver returns an [agent.Driver] that starts a Claude Code agent for
// each session. The options of the driver come first, and the fields of
// the config come after them.
//
// The driver maps Model to the ID of the model, Dir, System, Allow to
// [AllowedTools], Ask to [AskTools], Deny to [DisallowedTools], and
// Approve. It rejects Skills and Tools, because the CLI loads its own
// skills and runs only its own tools. It runs the hooks at the points of
// [Hooks]. Start rejects a hook at a model call, in the options of the
// driver or in the config.
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
	return listModels(ctx, newAgent(d.opts).cfg)
}

// Start starts the durable agent of the session c.Session.
func (d driver) Start(ctx context.Context, c agent.Config) (agent.Runner, error) {
	if len(c.Skills) > 0 {
		return nil, unsupported("Skills")
	}
	if len(c.Tools) > 0 {
		return nil, unsupported("Tools")
	}
	// The hooks of the driver come first, as its options do.
	var own config
	for _, opt := range d.opts {
		opt(&own)
	}
	hooks := append(slices.Clip(own.hooks), c.Hooks...)
	if err := agent.CheckHooks(hooks, hookPoints...); err != nil {
		return nil, fmt.Errorf("claude: %w", err)
	}

	opts := append([]Option(nil), d.opts...)
	if c.Store != nil {
		opts = append(opts, Store(c.Store))
	}
	if c.Model.ID != "" {
		opts = append(opts, Model(c.Model.ID))
	}
	if c.Dir != "" {
		opts = append(opts, Dir(c.Dir))
	}
	if c.System != "" {
		opts = append(opts, System(c.System))
	}
	if len(c.Allow) > 0 {
		opts = append(opts, AllowedTools(c.Allow...))
	}
	if len(c.Ask) > 0 {
		opts = append(opts, AskTools(c.Ask...))
	}
	if len(c.Deny) > 0 {
		opts = append(opts, DisallowedTools(c.Deny...))
	}
	if c.Approve != nil {
		opts = append(opts, Approve(c.Approve))
	}
	if len(c.Hooks) > 0 {
		opts = append(opts, Hooks(c.Hooks...))
	}
	return Durable(ctx, c.Session, opts...)
}

// unsupported returns the error of a config field that the driver
// cannot honor.
func unsupported(field string) error {
	return fmt.Errorf("claude: the config field %s: %w", field, errors.ErrUnsupported)
}
