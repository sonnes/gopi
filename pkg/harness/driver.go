package harness

import (
	"context"
	"fmt"
	"strings"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/def"
	"github.com/sonnes/gopi/pkg/harness/permission"
	storefs "github.com/sonnes/gopi/pkg/store/fs"
)

// Start starts the durable agent of the session c.Session, as
// [Harness.Durable] does, with the fields of c on top of the options of
// h. A harness is the default [agent.Driver] of a catalog: it runs a
// model of each text provider.
//
// The harness honors each field of c, each mode, and each point of a
// hook. It reads each folder of the Dirs of c, after the readers of h. The Mode of c replaces the [Mode] of h. The System of c goes
// after the [System] of h, as a CLI agent adds it to its own prompt.
// The Skills of c are preloaded, as with [Preload]. The rules of c go
// after the rules of h.
func (h *Harness) Start(ctx context.Context, c agent.Config) (agent.Runner, error) {
	if err := agent.CheckHooks(c.Hooks, agent.AllPoints...); err != nil {
		return nil, fmt.Errorf("harness: %w", err)
	}
	if err := agent.CheckMode(c.Mode, agent.AllModes...); err != nil {
		return nil, fmt.Errorf("harness: %w", err)
	}

	opts := []Option{
		Preload(c.Skills...),
		Allow(c.Allow...),
		Ask(c.Ask...),
		Deny(c.Deny...),
	}
	if c.Store != nil {
		opts = append(opts, Store(c.Store))
	}
	if c.Spec != "" {
		opts = append(opts, Model(c.Spec))
	}
	if c.Dir != "" {
		opts = append(opts, Dir(c.Dir))
	}
	if len(c.Dirs) > 0 {
		opts = append(opts, Defs(dirReaders(c.Dirs)...))
	}
	if c.Effort != "" {
		opts = append(opts, AgentDefaults(agent.Thinking(c.Effort)))
	}
	if c.Mode != "" {
		// The modes of a config have the names of the permission modes.
		opts = append(opts, Mode(permission.Mode(c.Mode)))
	}
	if c.System != "" {
		opts = append(opts, System(joinText(h.system, c.System)))
	}
	if c.Approve != nil {
		opts = append(opts, Approve(c.Approve))
	}
	if len(c.Hooks) > 0 {
		opts = append(opts, AgentDefaults(agent.Hooks(c.Hooks...)))
	}
	if len(c.Tools) > 0 {
		opts = append(opts, Toolset(Tools(c.Tools...)))
	}
	return h.Durable(ctx, c.Session, opts...)
}

// dirReaders returns a reader of each folder of dirs, in order.
func dirReaders(dirs []string) []def.Reader {
	readers := make([]def.Reader, len(dirs))
	for i, d := range dirs {
		readers[i] = storefs.Dir(d)
	}
	return readers
}

// joinText returns the texts that are not empty, joined by a blank
// line.
func joinText(texts ...string) string {
	var parts []string
	for _, t := range texts {
		if t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "\n\n")
}
