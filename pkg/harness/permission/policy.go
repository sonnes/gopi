package permission

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/sandbox/bash"
)

// Decision is the answer of an [ApproveFunc] to one tool call.
type Decision int

const (
	// Deny blocks the call. Deny is the zero value, so an approver that
	// returns no decision blocks the call.
	Deny Decision = iota
	// Allow runs this call only.
	Allow
	// AllowAlways runs this call, and each later call that the same rule
	// matches. For the bash tool, the hook keeps an Allow rule for the
	// exact command. For another tool, it keeps an Allow rule for the
	// tool name. The hook keeps the rule in memory for its life. An Ask
	// rule and a Deny rule still apply to the later calls.
	AllowAlways
)

// ApproveFunc asks for a decision on a tool call that needs approval,
// for example from the user. If it returns an error, the call does not
// run, and the text of the error is the error result.
type ApproveFunc func(ctx context.Context, call ai.ToolCall) (Decision, error)

// Policy holds the permission mode and the rules of a harness. Each
// rule has the format "Tool" or "Tool(content)". The package doc has
// the format and the order of the checks.
//
// A Policy is a value with no state, so a caller can copy it, for
// example to give a child agent a stricter Mode. The rules of
// [AllowAlways] are in the hook that [Policy.Hook] returns.
type Policy struct {
	// Mode decides the calls that no rule matches. The default is
	// [Default].
	Mode Mode
	// Allow lists the rules for the calls that run.
	Allow []string
	// Ask lists the rules for the calls that need approval, in each mode.
	Ask []string
	// Deny lists the rules for the calls that are blocked, in each mode.
	Deny []string
	// Approve decides each call that needs approval. If Approve is nil,
	// each call that needs approval is blocked, with a reason that says
	// that the policy has no approver.
	Approve ApproveFunc
	// Kinds gives the kind of a tool, for a custom tool or to change the
	// kind of a built-in tool. A tool that is not in Kinds and is not a
	// built-in tool is [Other].
	Kinds map[string]Kind
	// Dir is the working directory for the path rules and for a
	// relative path in the input of a tool. The default is the working
	// directory of the process when [Policy.Hook] is called.
	Dir string
}

// Validate returns an error for an unknown mode and for each rule that
// does not parse. The error names each bad rule.
func (p Policy) Validate() error {
	_, err := p.compile()
	return err
}

// compiled is a policy with its rules parsed.
type compiled struct {
	mode  Mode
	allow []rule
	ask   []rule
	deny  []rule
}

func (p Policy) compile() (compiled, error) {
	var errs []error
	mode, err := ParseMode(string(p.Mode))
	if err != nil {
		errs = append(errs, err)
	}

	parseAll := func(texts []string) []rule {
		rules := make([]rule, 0, len(texts))
		for _, text := range texts {
			r, err := parseRule(text)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			rules = append(rules, r)
		}
		return rules
	}

	c := compiled{
		mode:  mode,
		allow: parseAll(p.Allow),
		ask:   parseAll(p.Ask),
		deny:  parseAll(p.Deny),
	}
	return c, errors.Join(errs...)
}

// Hook returns an [agent.Hook] that checks each tool call with the
// policy in its BeforeTool method. The hook reads the fields of the
// policy once, in this call. A later change to a field does not change
// the hook. Each hook starts with no rules of [AllowAlways], and keeps
// them for its life.
//
// If the policy is not valid, or Dir is empty and the working
// directory is unknown, the hook blocks each call with the error.
// Call [Policy.Validate] first to get the error.
func (p Policy) Hook() agent.Hook {
	c, err := p.compile()
	dir := p.Dir
	if err == nil {
		dir, err = absDir(dir)
	}
	// A missing home directory makes each rule that starts with ~/
	// match no path.
	home, _ := os.UserHomeDir()

	kinds := make(map[string]Kind, len(p.Kinds))
	for name, k := range p.Kinds {
		kinds[name] = k
	}
	return &hook{
		compiled: c,
		err:      err,
		approve:  p.Approve,
		kinds:    kinds,
		dir:      dir,
		home:     home,
	}
}

// absDir returns the absolute path of dir. An empty dir is the working
// directory of the process.
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

// hook is the [agent.Hook] of a [Policy].
type hook struct {
	agent.BaseHook
	compiled
	// err blocks each call if it is set.
	err     error
	approve ApproveFunc
	kinds   map[string]Kind
	dir     string
	home    string

	mu sync.Mutex
	// kept are the Allow rules of [AllowAlways].
	kept []rule
}

// toolInput holds the inputs that the rules read.
type toolInput struct {
	// ok is false if the input is not a JSON object.
	ok      bool
	Command string `json:"command"`
	Path    string `json:"path"`
}

func decodeInput(raw json.RawMessage) toolInput {
	var in toolInput
	err := json.Unmarshal(raw, &in)
	in.ok = err == nil
	return in
}

// BeforeTool runs the checks of the policy on c. It returns c without
// change, or an [agent.DenyError] that blocks the call.
func (h *hook) BeforeTool(
	ctx context.Context,
	_ agent.State,
	c ai.ToolCall,
) (ai.ToolCall, error) {
	if h.err != nil {
		return c, agent.Deny(h.err.Error())
	}
	in := decodeInput(c.Input)

	denyRule := h.firstMatch(h.deny, c.Name, in)
	if denyRule != nil {
		reason := fmt.Sprintf("the rule %q denies the tool call %q", denyRule.text, c.Name)
		return c, agent.Deny(reason)
	}
	askRule := h.firstMatch(h.ask, c.Name, in)
	if askRule != nil {
		return h.requestApproval(ctx, c, in)
	}
	if h.allowed(c.Name, in) {
		return c, nil
	}

	kind := kindOf(c.Name, h.kinds)
	switch {
	case kind == Read || h.mode == Bypass:
		return c, nil
	case h.mode == Plan:
		reason := fmt.Sprintf(
			"plan mode blocks the tool call %q, because the tool can change the workspace",
			c.Name,
		)
		return c, agent.Deny(reason)
	case h.mode == AcceptEdits && kind == Edit:
		return c, nil
	}
	return h.requestApproval(ctx, c, in)
}

// requestApproval gets a decision on c from the approver.
func (h *hook) requestApproval(
	ctx context.Context,
	c ai.ToolCall,
	in toolInput,
) (ai.ToolCall, error) {
	if h.approve == nil {
		reason := fmt.Sprintf(
			"the tool call %q needs approval, and the policy has no approver",
			c.Name,
		)
		return c, agent.Deny(reason)
	}
	d, err := h.approve(ctx, c)
	if err != nil {
		return c, fmt.Errorf("permission: approve the tool call %q: %w", c.Name, err)
	}
	switch d {
	case Allow:
		return c, nil
	case AllowAlways:
		h.keep(c.Name, in)
		return c, nil
	}
	reason := fmt.Sprintf("the approver denied the tool call %q", c.Name)
	return c, agent.Deny(reason)
}

// keep adds the Allow rule of [AllowAlways] to the hook. For a bash
// call with an input that does not decode, it keeps no rule, because a
// rule for the tool name would allow each command.
func (h *hook) keep(name string, in toolInput) {
	r := rule{
		text:  name,
		names: []string{name},
	}
	if name == bash.Name {
		if !in.ok {
			return
		}
		command := strings.TrimSpace(in.Command)
		r.text = fmt.Sprintf("%s(%s)", name, command)
		r.hasContent = true
		r.command = shellRule{
			kind: shellExact,
			text: command,
		}
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	h.kept = append(h.kept, r)
}

// firstMatch returns the first Deny rule or Ask rule in rules that
// matches the call, or nil. If the input does not decode, a rule with
// content matches, so a bad input cannot pass a Deny rule.
func (h *hook) firstMatch(rules []rule, name string, in toolInput) *rule {
	for i := range rules {
		r := &rules[i]
		if !r.matchesName(name) {
			continue
		}
		if !r.hasContent || !in.ok {
			return r
		}
		if h.contentMatches(r, name, in) {
			return r
		}
	}
	return nil
}

func (h *hook) contentMatches(r *rule, name string, in toolInput) bool {
	if contentKinds[name] == commandContent {
		return commandMatches(in.Command, r.command)
	}
	abs := resolvePath(in.Path, h.dir, h.home)
	return r.path.matches(abs, h.dir, h.home)
}

// allowed reports if the Allow rules or the kept rules match the call.
// For the bash tool, the Allow rules together must match each part of
// the command. If the input does not decode, only a rule without
// content matches.
func (h *hook) allowed(name string, in toolInput) bool {
	h.mu.Lock()
	rules := append(h.allow[:len(h.allow):len(h.allow)], h.kept...)
	h.mu.Unlock()

	var commands []shellRule
	for i := range rules {
		r := &rules[i]
		if !r.matchesName(name) {
			continue
		}
		if !r.hasContent {
			return true
		}
		if !in.ok {
			continue
		}
		if contentKinds[name] == commandContent {
			commands = append(commands, r.command)
			continue
		}
		if h.contentMatches(r, name, in) {
			return true
		}
	}
	if len(commands) == 0 {
		return false
	}
	return commandAllowed(in.Command, commands)
}
