package def

import (
	"context"
	"errors"
	"fmt"
)

// ErrNotFound is the error of [FindAgent] when no reader gives an
// agent with the name.
var ErrNotFound = errors.New("def: not found")

// FindAgent reads each reader in order and returns the last agent with
// the name. A later agent wins, as in the merge of the harness. An
// error of a reader stops the search.
func FindAgent(ctx context.Context, name string, readers ...AgentsReader) (Agent, error) {
	var (
		found Agent
		ok    bool
	)
	for i, r := range readers {
		agents, err := r.ReadAgents(ctx)
		if err != nil {
			return Agent{}, fmt.Errorf("def: agents reader %d (%T): %w", i, r, err)
		}
		for _, a := range agents {
			if a.Name == name {
				found = a
				ok = true
			}
		}
	}
	if !ok {
		return Agent{}, fmt.Errorf("agent %q: %w", name, ErrNotFound)
	}
	return found, nil
}
