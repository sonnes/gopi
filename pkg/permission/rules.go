package permission

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sonnes/gopi/pkg/ai"
)

// Rules is one layer of permission rules, such as the rules of a
// workspace, of a project, or of a session. The package doc gives the
// format of a rule.
type Rules struct {
	// Allow lists the rules for the calls that run with no person.
	Allow []string `json:"allow,omitempty"`
	// Ask lists the rules for the calls that wait for a person, in each
	// mode.
	Ask []string `json:"ask,omitempty"`
	// Deny lists the rules for the calls that never run, in each mode.
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
	_, err := r.parse()
	return err
}

// CheckRule returns an error that names the rule if the rule does not
// parse.
func CheckRule(rule string) error {
	_, err := parseRule(rule)
	return err
}

// Match is what a set of [Rules], or a [Policy], says about one tool
// call.
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
// The rules match as in [Policy.Decide], with dir as [Policy.Dir]: a
// path that starts with "/" is from dir. Check returns an error that
// names each rule that does not parse.
func (r Rules) Check(c ai.ToolCall, dir string) (Match, string, error) {
	m, err := r.compile(dir)
	if err != nil {
		return NoMatch, "", err
	}
	match, text := m.match(c.Name, decodeInput(c.Input))
	return match, text, nil
}

// parsed holds the rules of a [Rules] after the parse.
type parsed struct {
	allow []rule
	ask   []rule
	deny  []rule
}

func (r Rules) parse() (parsed, error) {
	var errs []error
	parseAll := func(texts []string) []rule {
		rules := make([]rule, 0, len(texts))
		for _, text := range texts {
			parsedRule, err := parseRule(text)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			rules = append(rules, parsedRule)
		}
		return rules
	}

	p := parsed{
		allow: parseAll(r.Allow),
		ask:   parseAll(r.Ask),
		deny:  parseAll(r.Deny),
	}
	return p, errors.Join(errs...)
}

// matcher matches the parsed rules on the calls of an agent in a place.
type matcher struct {
	parsed
	place
}

func (r Rules) compile(dir string) (matcher, error) {
	p, err := r.parse()
	if err != nil {
		return matcher{}, err
	}
	pl, err := newPlace(dir)
	if err != nil {
		return matcher{}, err
	}
	m := matcher{
		parsed: p,
		place:  pl,
	}
	return m, nil
}

// match holds the order of the rules for [Rules.Check] and
// [Policy.Decide]: the first Deny rule that matches, then the first Ask
// rule, then the Allow rules. It returns the text of the Deny rule or
// the Ask rule.
func (m matcher) match(name string, in toolInput) (Match, string) {
	if r := m.firstMatch(m.deny, name, in); r != nil {
		return DenyMatch, r.text
	}
	if r := m.firstMatch(m.ask, name, in); r != nil {
		return AskMatch, r.text
	}
	if m.allowed(name, in) {
		return AllowMatch, ""
	}
	return NoMatch, ""
}

// place is where the paths of the calls of an agent resolve.
type place struct {
	// dir is the absolute folder of a relative path.
	dir string
	// home is the folder of a path that starts with ~/.
	home string
}

// newPlace returns the place with the folder dir. An empty dir is the
// working directory of the process.
func newPlace(dir string) (place, error) {
	abs, err := absDir(dir)
	if err != nil {
		return place{}, err
	}
	// A missing home directory makes each rule that starts with ~/
	// match no path.
	home, _ := os.UserHomeDir()
	pl := place{
		dir:  abs,
		home: home,
	}
	return pl, nil
}

func absDir(dir string) (string, error) {
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("permission: find the working directory: %w", err)
		}
		return wd, nil
	}
	return filepath.Abs(dir)
}
