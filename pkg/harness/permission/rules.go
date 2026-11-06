package permission

import (
	"errors"
	"slices"

	"github.com/sonnes/gopi/pkg/ai"
)

// Rules is one layer of permission rules, such as the rules of a
// workspace, of a project, or of a session. The package doc gives the
// format of a rule.
type Rules struct {
	// Allow lists the rules for the calls that run with no person.
	Allow []string `json:"allow,omitempty"`
	// Ask lists the rules for the calls that wait for a person.
	Ask []string `json:"ask,omitempty"`
	// Deny lists the rules for the calls that never run.
	Deny []string `json:"deny,omitempty"`
}

// Merge returns the rules of each layer in one value, in the order of
// the layers. The order does not change the result of [Rules.Check],
// because a Deny rule of any layer wins, then an Ask rule, then an
// Allow rule.
func Merge(layers ...Rules) Rules {
	var out Rules
	for _, l := range layers {
		out.Allow = append(out.Allow, l.Allow...)
		out.Ask = append(out.Ask, l.Ask...)
		out.Deny = append(out.Deny, l.Deny...)
	}
	return out
}

// Validate returns an error that names each rule of r that does not
// parse.
func (r Rules) Validate() error {
	var errs []error
	for _, text := range slices.Concat(r.Deny, r.Ask, r.Allow) {
		errs = append(errs, CheckRule(text))
	}
	return errors.Join(errs...)
}

// CheckRule returns an error that names the rule if the rule does not
// parse.
func CheckRule(rule string) error {
	_, err := parseRule(rule)
	return err
}

// Match is what a set of [Rules] says about one tool call.
type Match int

const (
	// NoMatch is a call that no rule matches. The mode of the agent
	// decides it.
	NoMatch Match = iota
	// AllowMatch is a call that runs with no person.
	AllowMatch
	// AskMatch is a call that waits for a person.
	AskMatch
	// DenyMatch is a call that never runs.
	DenyMatch
)

// Check returns what the rules of r say about the call c, and the text
// of the Deny rule or the Ask rule that matches. A Deny rule wins, then
// an Ask rule, then the Allow rules. For a bash call, the Allow rules
// together must match each part of the command, so no one rule text
// comes back for an Allow match.
//
// The rules match as in the hook of [Policy.Hook], with dir as
// [Policy.Dir]: a path that starts with "/" is from dir. Check returns
// an error that names each rule that does not parse.
func (r Rules) Check(c ai.ToolCall, dir string) (Match, string, error) {
	p := Policy{
		Allow: r.Allow,
		Ask:   r.Ask,
		Deny:  r.Deny,
		Dir:   dir,
	}
	h := p.Hook().(*hook)
	if h.err != nil {
		return NoMatch, "", h.err
	}

	in := decodeInput(c.Input)
	if d := h.firstMatch(h.deny, c.Name, in); d != nil {
		return DenyMatch, d.text, nil
	}
	if a := h.firstMatch(h.ask, c.Name, in); a != nil {
		return AskMatch, a.text, nil
	}
	if h.allowed(c.Name, in) {
		return AllowMatch, "", nil
	}
	return NoMatch, "", nil
}
