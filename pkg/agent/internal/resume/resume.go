// Package resume keeps the ID that an agent CLI gives its conversation,
// such as the session of Claude Code or the thread of Codex. The ID is
// a [session.Value], so a Durable agent over the same session resumes
// the same conversation of the CLI. A CLI agent reads and writes the ID
// only through an [ID], and a caller of the agent never sees it.
package resume

import (
	"context"

	"github.com/sonnes/gopi/pkg/session"
)

// namespace is the namespace of the IDs of every CLI agent.
const namespace = "gopi.cli"

// ID is the stored conversation ID of one CLI.
type ID struct {
	value session.Value[string]
}

// New returns the ID of the CLI named cli, such as "claude". Each CLI
// has its own key, so two CLI agents can share a session.
func New(cli string) ID {
	return ID{value: session.Value[string]{
		Namespace: namespace,
		Key:       cli,
	}}
}

// Get returns the stored ID, or "" if the session has none.
func (i ID) Get(r session.Reader) (string, error) {
	id, _, err := i.value.Get(r)
	return id, err
}

// Save stores id. An empty id or the stored id does nothing, so a line
// of the CLI with no ID does not clear the stored one. The write
// ignores the cancel of ctx, because the CLI keeps the conversation
// either way.
func (i ID) Save(ctx context.Context, w session.Writer, id string) error {
	if id == "" {
		return nil
	}
	old, err := i.Get(w)
	if err != nil {
		return err
	}
	if old == id {
		return nil
	}
	return i.value.Set(context.WithoutCancel(ctx), w, id)
}

// Clear removes the stored ID, so that the next run starts a new
// conversation of the CLI. The write ignores the cancel of ctx, as in
// [ID.Save].
func (i ID) Clear(ctx context.Context, w session.Writer) error {
	return i.value.Delete(context.WithoutCancel(ctx), w)
}
