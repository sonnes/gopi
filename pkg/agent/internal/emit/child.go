package emit

import (
	"context"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/session"
)

// OpenChild returns the log of the child session id in store, and writes
// ref as its [agent.Parent] when the log is new. A child that exists,
// such as one that a replay finds again, keeps its log. If id is "" or
// store is nil, the parent is unsaved, and the child gets an unsaved log.
func OpenChild(ctx context.Context, store session.Storer, id string, ref agent.ParentRef) (*session.Log, error) {
	if id == "" || store == nil {
		return session.New(), nil
	}
	log, err := session.Open(ctx, store, id)
	if err != nil {
		return nil, err
	}
	if len(log.Entries()) > 0 {
		return log, nil
	}
	if err := agent.Parent.Set(ctx, log.Writer(), ref); err != nil {
		return nil, err
	}
	return log, nil
}
