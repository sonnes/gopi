package landing

import (
	"context"

	"github.com/sonnes/gopi/pkg/catalog"
	"github.com/sonnes/gopi/pkg/host"
)

// Manage is the preview of the managed agents band: one host runs two
// sessions, each with its own workspace and its own kind of agent, and
// reads the status of every session.
func Manage(
	ctx context.Context,
	cat *catalog.Catalog,
	store host.Storer,
) error {
	// snippet:host
	h, err := host.Open(ctx, cat, host.Store(store))
	if err != nil {
		return err
	}

	// Each session works in its own folder, with its own kind of agent.
	fix, err := h.Create(ctx, host.Config{
		Model: "claude-code/claude-opus-5-5",
		Dir:   "repos/app",
	})
	if err != nil {
		return err
	}
	notes, err := h.Create(ctx, host.Config{
		Model: "codex/gpt-6-astra",
		Dir:   "repos/site",
		Dirs:  []string{"repos/site/.agents"},
	})
	if err != nil {
		return err
	}

	h.Send(ctx, fix.ID, host.Text("Fix the failing test."))
	h.Send(ctx, notes.ID, host.Text("Write the release notes."))

	// The status of every session, then each change of it.
	state, events := h.Events(ctx)
	// end
	_, _ = state, events
	return nil
}
