package host

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/permission"
	"github.com/sonnes/gopi/pkg/session"
)

// grantsValue is the session value with the rule of each Always
// decision of a person, in order.
var grantsValue = session.Value[[]string]{Namespace: "gopi.permission", Key: "grants"}

// RulesReader gives the permission rules of a session that come from
// outside the host, such as the rules of a workspace and of a project.
// See [Rules].
type RulesReader interface {
	// Rules returns the rules of the session id. An error blocks the
	// tool call that needs the rules.
	Rules(ctx context.Context, sessionID string) (permission.Rules, error)
}

// Rules sets the reader of the rules of each session. The host calls r
// at each tool call that it checks, and never keeps the result, so a
// change of the rules reaches the next call. Without Rules, a session
// has the rules of its [Config] and its grants.
//
// The rules of a call are the rules of the config, the rules of r, and
// the grants of the session, in one [permission.Rules]. A Deny rule of
// any of them wins, then an Ask rule, then an Allow rule. A rule path
// that starts with "/" is in the Dir of the config.
//
// A grant is the rule of an Always decision. The host keeps the grants
// in the session value "gopi.permission" "grants", so they stay after a
// restart.
func Rules(r RulesReader) Option {
	return func(h *Host) { h.rules = r }
}

// needsRulesHook reports if a session with config can have a Deny rule
// or an Ask rule. The rules hook then checks each call before it runs.
// An Allow rule and a grant need no hook, because the host checks them
// in approve. A session with no hook needs no hook command on Codex.
func (h *Host) needsRulesHook(config Config) bool {
	return h.rules != nil || len(config.Ask) > 0 || len(config.Deny) > 0
}

// rulesHook returns a BeforeTool hook that checks each tool call with
// the Deny rules and the Ask rules of s. A Deny rule blocks the call. An
// Ask rule returns an [agent.AskError], and the agent asks its approve
// function.
func rulesHook(s *hosted) agent.Hook {
	return agent.BeforeTool(s.checkRules)
}

// checkRules blocks the call c if a Deny rule of s matches it, and asks
// about c if an Ask rule matches it.
func (s *hosted) checkRules(ctx context.Context, _ agent.State, c ai.ToolCall) (ai.ToolCall, error) {
	d, _, err := s.decide(ctx, c, false)
	if err != nil {
		return c, agent.Deny(err.Error())
	}
	switch d.Match {
	case permission.DenyMatch:
		return c, agent.Deny(d.Reason)
	case permission.AskMatch:
		return c, agent.Ask(d.Reason)
	}
	return c, nil
}

// decide returns what the rules of s say about the call c, and the
// folder of the rule paths. A Deny match and an Ask match have a reason.
// A call that a person allowed in the active run gets
// [permission.AllowMatch]. Otherwise an Allow rule does not approve a
// safety call, which gets [permission.AskMatch] with Safety set. The
// rules hook and [hosted.approve] both call decide, so they check a call
// in the same order.
func (s *hosted) decide(ctx context.Context, c ai.ToolCall, safety bool) (permission.Decision, string, error) {
	if s.personAllowed(c) {
		return permission.Decision{Match: permission.AllowMatch}, "", nil
	}
	rules, dir, err := s.ruleSet(ctx)
	if err != nil {
		return permission.Decision{}, "", err
	}
	match, rule, err := rules.Check(c, dir)
	if err != nil {
		return permission.Decision{}, "", fmt.Errorf("host: session %s: %w", s.id, err)
	}
	d := permission.Decision{Match: match}
	switch {
	case match == permission.DenyMatch:
		d.Reason = permission.DenyReason(rule, c.Name)
	case match == permission.AskMatch:
		d.Reason = fmt.Sprintf("the rule %q asks before the tool call %q", rule, c.Name)
	case match == permission.AllowMatch && safety:
		d.Match = permission.AskMatch
		d.Safety = true
	}
	return d, dir, nil
}

// ruleSet returns the rules of a call of s, and the folder of a rule
// path that starts with "/".
func (s *hosted) ruleSet(ctx context.Context) (permission.Rules, string, error) {
	s.mu.Lock()
	config := s.rules
	dir := s.dir
	s.mu.Unlock()

	var read permission.Rules
	if r := s.host.rules; r != nil {
		var err error
		read, err = r.Rules(ctx, s.id)
		if err != nil {
			return permission.Rules{}, "", fmt.Errorf("host: session %s: read the rules: %w", s.id, err)
		}
	}
	kept, err := s.grants(ctx)
	if err != nil {
		return permission.Rules{}, "", err
	}
	all := permission.Merge(config, read, permission.Rules{Allow: kept})
	return all, dir, nil
}

// grants returns the grants of s.
func (s *hosted) grants(ctx context.Context) ([]string, error) {
	log, err := s.log(ctx)
	if err != nil {
		return nil, err
	}
	kept, _, err := grantsValue.Get(log)
	if err != nil {
		return nil, fmt.Errorf("host: session %s: read the grants: %w", s.id, err)
	}
	return kept, nil
}

// addGrant adds rule to the grants of s, and writes them to the store.
func (s *hosted) addGrant(ctx context.Context, rule string) error {
	s.grantMu.Lock()
	defer s.grantMu.Unlock()
	log, err := s.log(ctx)
	if err != nil {
		return err
	}
	kept, _, err := grantsValue.Get(log)
	if err != nil {
		return fmt.Errorf("host: session %s: read the grants: %w", s.id, err)
	}
	if slices.Contains(kept, rule) {
		return nil
	}
	err = grantsValue.Set(ctx, log, append(kept, rule))
	if err != nil {
		return fmt.Errorf("host: session %s: keep the grant %q: %w", s.id, rule, err)
	}
	return nil
}

// allowCall records that a person allowed the call id with input in
// the active run.
func (s *hosted) allowCall(id string, input json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.allowed == nil {
		s.allowed = make(map[string]json.RawMessage)
	}
	s.allowed[id] = input
}

// personAllowed reports if a person allowed c, with the same input, in
// the active run. The rules hook and an agent can ask about one call, so
// the second question gets the answer of the first.
func (s *hosted) personAllowed(c ai.ToolCall) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	input, ok := s.allowed[c.ID]
	return ok && bytes.Equal(input, c.Input)
}
