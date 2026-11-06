package permission

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/sandbox/bash"
	"github.com/sonnes/gopi/pkg/tool"
)

// Policy holds the permission mode and the rules of a harness. Each
// rule has the format "Tool" or "Tool(content)". The package doc has
// the format and the order of the checks.
//
// A Policy is a value with no state, so a caller can copy it, for
// example to give a child agent a stricter Mode. The rules of the
// Always decisions are in the hook that [Policy.Hook] returns.
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
	//
	// If the decision allows the call with Always, the hook keeps an
	// Allow rule. For the bash tool, the rule is for the exact command.
	// For another tool, the rule is for the tool name. The hook keeps
	// the rule in memory for its life. An Ask rule and a Deny rule
	// still apply to the later calls. An Input replaces the input of the
	// call. The Deny rules check the new input, and a match denies the
	// call. An Always rule is for the new input. A Message is the reason
	// of a deny. If Approve returns an error, the call does not run, and
	// the text of the error is the error result.
	Approve agent.ApproveFunc
	// Kinds gives the kind of a tool, for a custom tool or to change the
	// kind of a built-in tool. A tool that is not in Kinds gets its kind
	// from the registry of [tool.Lookup], and a tool that is in
	// neither is [tool.KindOther]. The policy finds the read-only
	// commands only for a bash tool of the kind [tool.KindExec].
	Kinds map[string]tool.Kind
	// Dir is the working directory for the path rules and for a
	// relative path in the input of a tool. The default is the working
	// directory of the process when [Policy.Hook] is called.
	Dir string
	// Roots are the folders where a read runs without a question, and
	// where an edit runs in [AcceptEdits]. A relative root is relative to
	// Dir. The default is Dir alone.
	Roots []string
	// BlockReadsOutside denies a read of a file tool outside Roots, in
	// each mode, also in [Bypass].
	BlockReadsOutside bool
	// BlockWritesOutside denies an edit of a file tool outside Roots, in
	// each mode, also in [Bypass].
	BlockWritesOutside bool
}

// Allows reports if an Allow rule of p matches c, as the hook of
// [Policy.Hook] checks it. It ignores the mode, the Ask rules, and the
// Deny rules. A driver whose agent asks on its own uses it to answer
// the requests that an Allow rule covers. A policy that is not valid
// allows no call.
func (p Policy) Allows(c ai.ToolCall) bool {
	h, ok := p.Hook().(*hook)
	if !ok || h.err != nil {
		return false
	}
	return h.allowed(c.Name, decodeInput(c.Input))
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
// policy in its BeforeTool method. The hook resolves the symlinks of the
// roots once, in this call, and the symlinks of each path on each call.
// The hook reads the fields of the policy once, in this call. A later
// change to a field does not change the hook. Each hook starts with no
// rules of the Always decisions, and keeps them for its life.
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

	roots := p.Roots
	if len(roots) == 0 {
		roots = []string{dir}
	}
	return &hook{
		compiled:    c,
		err:         err,
		approve:     p.Approve,
		kinds:       maps.Clone(p.Kinds),
		dir:         dir,
		home:        home,
		roots:       newRootSet(roots, dir),
		blockReads:  p.BlockReadsOutside,
		blockWrites: p.BlockWritesOutside,
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
	approve agent.ApproveFunc
	kinds   map[string]tool.Kind
	dir     string
	home    string
	roots   rootSet
	// blockReads and blockWrites are the hard limits of the policy.
	blockReads  bool
	blockWrites bool

	mu sync.Mutex
	// kept are the Allow rules of the Always decisions.
	kept []rule
}

// toolInput holds the inputs that the rules read. A file tool of
// Claude Code names its path file_path or notebook_path, and a tool of
// gopi names it path.
type toolInput struct {
	// ok is false if the input is not a JSON object.
	ok           bool
	Command      string `json:"command"`
	Path         string `json:"path"`
	FilePath     string `json:"file_path"`
	NotebookPath string `json:"notebook_path"`
	// URL is the input of web_fetch.
	URL string `json:"url"`
	// Changes are the file changes of an apply_patch call, as the Codex
	// agent gives them.
	Changes []struct {
		Path string `json:"path"`
	} `json:"changes"`
}

// The headers of the patch text of apply_patch that name a path.
var patchHeaders = []string{
	"*** Add File: ",
	"*** Update File: ",
	"*** Delete File: ",
	"*** Move to: ",
}

// paths returns the paths that a call of the tool name reads or
// changes. An apply_patch call has a path for each file of its changes
// or of its patch text. Each other tool has its path input.
func (in toolInput) paths(name string) []string {
	if name != tool.ApplyPatch {
		p := cmp.Or(in.Path, in.FilePath, in.NotebookPath)
		return []string{p}
	}
	var out []string
	for _, c := range in.Changes {
		out = append(out, c.Path)
	}
	for _, line := range strings.Split(in.Command, "\n") {
		for _, h := range patchHeaders {
			if p, ok := strings.CutPrefix(line, h); ok {
				out = append(out, strings.TrimSpace(p))
			}
		}
	}
	return out
}

func decodeInput(raw json.RawMessage) toolInput {
	var in toolInput
	err := json.Unmarshal(raw, &in)
	in.ok = err == nil
	return in
}

// BeforeTool runs the checks of the policy on c. It returns c, c with
// the input that the approver gave, or an [agent.DenyError] that blocks
// the call.
func (h *hook) BeforeTool(
	ctx context.Context,
	_ agent.State,
	c ai.ToolCall,
) (ai.ToolCall, error) {
	if h.err != nil {
		return c, agent.Deny(h.err.Error())
	}
	in := decodeInput(c.Input)
	targets, resolved := h.targets(c.Name, in)
	if !resolved {
		reason := fmt.Sprintf(
			"the tool call %q has a path with symlinks that do not resolve. Give the real path",
			c.Name,
		)
		return c, agent.Deny(reason)
	}

	denyRule := h.firstMatch(h.deny, c.Name, in)
	if denyRule != nil {
		reason := fmt.Sprintf("the rule %q denies the tool call %q", denyRule.text, c.Name)
		return c, agent.Deny(reason)
	}
	kind := kindOf(c.Name, h.kinds)
	if err := h.checkLimits(c.Name, kind, targets); err != nil {
		return c, err
	}
	askRule := h.firstMatch(h.ask, c.Name, in)
	if askRule != nil {
		return h.requestApproval(ctx, c, in)
	}
	command := h.readOnly(c.Name, kind, in)
	if h.blockReads && command == readsOutside {
		return h.requestApproval(ctx, c, in)
	}
	// An Allow rule does not approve an edit of a protected path.
	editsProtected := kind == tool.KindEdit && protected(targets)
	if editsProtected && h.mode != Bypass {
		return h.requestApproval(ctx, c, in)
	}
	if h.allowed(c.Name, in) {
		return c, nil
	}

	inRoots := h.inRoots(targets)
	switch {
	case h.mode == Bypass:
		return c, nil
	case kind == tool.KindRead && inRoots:
		return c, nil
	case kind == tool.KindRead:
		return h.requestApproval(ctx, c, in)
	case command == readsInside:
		return c, nil
	case command == readsOutside:
		return h.requestApproval(ctx, c, in)
	case h.mode == AcceptEdits && kind == tool.KindEdit && inRoots:
		return c, nil
	}
	return h.requestApproval(ctx, c, in)
}

// targets returns the spellings of each path of a call of a file tool.
// A call of another tool has no targets. ok is false if the symlinks of
// a path do not resolve.
func (h *hook) targets(name string, in toolInput) (targets [][]string, ok bool) {
	if contentOf(name) != tool.ContentPath || !in.ok {
		return nil, true
	}
	for _, p := range in.paths(name) {
		spellings, ok := spellingsOf(resolvePath(p, h.dir, h.home))
		if !ok {
			return nil, false
		}
		targets = append(targets, spellings)
	}
	return targets, true
}

// readOnlyResult is what [hook.readOnly] finds in a bash command.
type readOnlyResult int

const (
	// notReadOnly is a command that can change something, or a command
	// that the parser does not know.
	notReadOnly readOnlyResult = iota
	// readsInside is a read-only command whose paths are in the roots.
	readsInside
	// readsOutside is a read-only command with a path outside the roots.
	readsOutside
)

// readOnly reports if a call of the bash tool is a read-only command,
// and if each path that it reads is in the roots. A path whose symlinks
// do not resolve is outside. A tool of another name or kind is
// notReadOnly.
func (h *hook) readOnly(name string, kind tool.Kind, in toolInput) readOnlyResult {
	if name != bash.Name || kind != tool.KindExec || !in.ok {
		return notReadOnly
	}
	paths, ok := readOnlyPaths(in.Command, h.dir, h.home)
	if !ok {
		return notReadOnly
	}
	for _, p := range paths {
		spellings, ok := spellingsOf(p)
		if !ok || !h.roots.contains(spellings) {
			return readsOutside
		}
	}
	return readsInside
}

// inRoots reports if each spelling of each target is in a root.
func (h *hook) inRoots(targets [][]string) bool {
	for _, spellings := range targets {
		if !h.roots.contains(spellings) {
			return false
		}
	}
	return true
}

// checkLimits returns an [agent.DenyError] if a hard limit of the policy
// blocks a call of the kind with the targets.
func (h *hook) checkLimits(name string, kind tool.Kind, targets [][]string) error {
	var limit string
	switch {
	case kind == tool.KindRead && h.blockReads:
		limit = "reads"
	case kind == tool.KindEdit && h.blockWrites:
		limit = "writes"
	default:
		return nil
	}
	for _, spellings := range targets {
		if h.roots.contains(spellings) {
			continue
		}
		reason := fmt.Sprintf(
			"the tool call %q is denied: the path %q is outside the roots of the session, and the policy blocks %s outside them",
			name,
			spellings[0],
			limit,
		)
		return agent.Deny(reason)
	}
	return nil
}

// protected reports if a spelling of a target is a protected path.
func protected(targets [][]string) bool {
	for _, spellings := range targets {
		if slices.ContainsFunc(spellings, isProtected) {
			return true
		}
	}
	return false
}

// requestApproval gets a decision on c from the approver. If the
// decision edits the input, the Deny rules check the edited input, and
// an Always decision keeps a rule for the edited input. The edited input
// is the input that runs.
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
	d, err := h.approve(ctx, agent.ApprovalRequest{Call: c})
	if err != nil {
		return c, fmt.Errorf("permission: approve the tool call %q: %w", c.Name, err)
	}
	if !d.Allow {
		reason := d.Message
		if reason == "" {
			reason = fmt.Sprintf("the approver denied the tool call %q", c.Name)
		}
		return c, agent.Deny(reason)
	}
	if d.Input != nil {
		c.Input = d.Input
		in = decodeInput(d.Input)
		denyRule := h.firstMatch(h.deny, c.Name, in)
		if denyRule != nil {
			reason := fmt.Sprintf("the rule %q denies the edited tool call %q", denyRule.text, c.Name)
			return c, agent.Deny(reason)
		}
	}
	if d.Always {
		h.keep(c.Name, in)
	}
	return c, nil
}

// keep adds the Allow rule of an Always decision to the hook. For a bash
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

// contentMatches reports if the content of r matches the input of the
// call. A path rule matches a call with several paths if it matches one
// spelling of one path, and a call with no path. So a Deny rule and an
// Ask rule cannot miss a path, a symlink, or a change of letter case.
// [hook.allowed] needs each spelling of each path instead. A domain
// rule matches a URL with no host, for the same reason.
func (h *hook) contentMatches(r *rule, name string, in toolInput) bool {
	switch contentOf(name) {
	case tool.ContentCommand:
		return commandMatches(in.Command, r.command)
	case tool.ContentDomain:
		host, ok := hostOf(in.URL)
		return !ok || host == r.domain.host
	}
	paths := in.paths(name)
	if len(paths) == 0 {
		return true
	}
	for _, p := range paths {
		spellings, _ := spellingsOf(resolvePath(p, h.dir, h.home))
		for _, s := range spellings {
			for _, f := range forms(s) {
				if r.path.matches(f, h.dir, h.home, true) {
					return true
				}
			}
		}
	}
	return false
}

// eachPathMatches reports if the content of r matches each spelling of
// each path of the input. A call with no path, or with a path whose
// symlinks do not resolve, matches no Allow rule. A spelling matches if
// one of its [forms] matches, so a rule on /tmp matches /private/tmp on
// macOS.
func (h *hook) eachPathMatches(r *rule, name string, in toolInput) bool {
	paths := in.paths(name)
	if len(paths) == 0 {
		return false
	}
	for _, p := range paths {
		spellings, ok := spellingsOf(resolvePath(p, h.dir, h.home))
		if !ok {
			return false
		}
		for _, s := range spellings {
			matches := slices.ContainsFunc(forms(s), func(f string) bool {
				return r.path.matches(f, h.dir, h.home, false)
			})
			if !matches {
				return false
			}
		}
	}
	return true
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
		switch contentOf(name) {
		case tool.ContentCommand:
			commands = append(commands, r.command)
			continue
		case tool.ContentDomain:
			host, ok := hostOf(in.URL)
			if ok && host == r.domain.host {
				return true
			}
			continue
		}
		if h.eachPathMatches(r, name, in) {
			return true
		}
	}
	if len(commands) == 0 {
		return false
	}
	return commandAllowed(in.Command, commands)
}
