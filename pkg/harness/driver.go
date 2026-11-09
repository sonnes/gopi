package harness

import (
	"strings"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/compact"
	"github.com/sonnes/gopi/pkg/permission"
)

// autoCompactFraction is the part of the context window that also starts
// a compaction, so the setting never passes a small window.
const autoCompactFraction = 0.8

// configOptions returns the agent options of c: the effort, the hooks,
// and the compaction.
func configOptions(c agent.Config) []agent.Option {
	var opts []agent.Option
	if c.Effort != "" {
		opts = append(opts, agent.Thinking(c.Effort))
	}
	if len(c.Hooks) > 0 {
		opts = append(opts, agent.Hooks(c.Hooks...))
	}
	if c.Compaction != nil {
		opts = append(opts, agent.Compaction(c.Compaction))
	} else if c.AutoCompact > 0 {
		strategy := compact.Summarize(compact.Limit(c.AutoCompact, autoCompactFraction))
		opts = append(opts, agent.Compaction(strategy))
	}
	return opts
}

// policy returns the permission policy of the call. dir is the folder
// of the default sandbox. The policy resolves a relative path in the
// folder of the sandbox, as the tools do.
func (h *Harness) policy(dir string) permission.Policy {
	dir = h.boxDir(dir)
	return permission.Policy{
		Mode: h.cfg.Mode,
		Rules: permission.Rules{
			Allow: h.allow,
			Deny:  h.cfg.Deny,
		},
		Limits: h.cfg.Limits,
		Dir:    dir,
		Roots:  h.roots(dir),
	}
}

// roots returns the folders that the agent works in: the folder of the
// sandbox, then the AddDirs of the config. The fence of [Fence] writes
// in them, and the permission policy reads and edits in them without a
// question. dir is the folder of the default sandbox.
func (h *Harness) roots(dir string) []string {
	return append([]string{h.boxDir(dir)}, h.cfg.AddDirs...)
}

// boxDir returns the folder of the sandbox of the tools: the Dir of the
// sandbox of [Sandbox], or dir, the folder of the default sandbox.
func (h *Harness) boxDir(dir string) string {
	if h.box != nil {
		return h.box.Dir()
	}
	return dir
}

// inFence reports whether the default sandbox runs in a fence.
func (h *Harness) inFence() bool {
	return h.fenced || h.cfg.Limits.Fence
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
