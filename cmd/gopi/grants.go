package main

import (
	"context"
	"sync"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/harness/permission"
)

// grants keeps the rule of each Always answer of a person for the life
// of the process. The harness keeps no rule for Always, so the CLI
// keeps them. A call that a grant allows runs with no question, unless
// the call is a safety check.
type grants struct {
	// dir is the folder of a relative path in a call.
	dir string
	// ask asks the person.
	ask agent.ApproveFunc

	mu    sync.Mutex
	rules []string
}

// Approve allows a call that a grant allows. Otherwise it gives the
// request the exact rule of the call, as [permission.Exact] gives it,
// and asks the person. If the person allows the call always, Approve
// keeps the rule. A safety check gets no rule, so Always on it keeps
// none. The decision that Approve returns has no Always.
func (g *grants) Approve(ctx context.Context, req agent.ApprovalRequest) (agent.Decision, error) {
	if !req.Safety && g.allows(req) {
		return agent.Decision{Allow: true}, nil
	}
	req.Rule = ""
	if !req.Safety {
		req.Rule, _ = permission.Exact(req.Call, g.dir)
	}
	d, err := g.ask(ctx, req)
	if err != nil {
		return agent.Decision{}, err
	}
	keep := d.Allow && d.Always && req.Rule != "" && d.Input == nil
	if keep {
		g.mu.Lock()
		g.rules = append(g.rules, req.Rule)
		g.mu.Unlock()
	}
	d.Always = false
	return d, nil
}

// allows reports if a grant allows the call of req.
func (g *grants) allows(req agent.ApprovalRequest) bool {
	g.mu.Lock()
	rules := permission.Rules{Allow: append([]string(nil), g.rules...)}
	g.mu.Unlock()
	match, _, err := rules.Check(req.Call, g.dir)
	return err == nil && match == permission.AllowMatch
}
