package permission

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/tool"
)

// Policy holds the permission mode, the rules, and the limits of an
// agent. The package doc has the order of the checks.
//
// A Policy is a value with no state, so a caller can copy it, for
// example to give a child agent a stricter Mode.
//
// A host gives the harness only its Deny rules. Its own hook checks
// the Allow rules and the Ask rules of the session, after the hook that
// the harness builds from the Policy. The Policy still asks for the calls that its Mode asks
// for, and the host answers them with its rules first. If both hooks
// ask about one call, the host asks the person once.
type Policy struct {
	// Mode decides the calls that no rule matches. The default is
	// [Default].
	Mode Mode
	// Rules are the Allow, Ask, and Deny rules.
	Rules Rules
	// Limits are the hard limits. The policy reads BlockReadsOutside and
	// BlockWritesOutside. Fence is for the sandbox that runs the
	// commands, and the policy ignores it.
	Limits Limits
	// Dir is the working directory for the path rules and for a
	// relative path in the input of a tool. The default is the working
	// directory of the process.
	Dir string
	// Roots are the folders where a read runs without a question, and
	// where an edit runs in [AcceptEdits]. A relative root is relative to
	// Dir. The default is Dir alone.
	Roots []string
}

// Limits are hard limits on the paths of an agent. A limit holds in each
// mode, also in [Bypass]. The roots are the working directory of the
// agent and the folders that the agent adds to it. Each driver maps a
// limit to its own control, and a driver whose agent has no such
// control fails at the start.
type Limits struct {
	// BlockReadsOutside refuses a read of a file tool outside the roots.
	// The harness denies the call, and a read-only bash command outside
	// the roots asks. Claude Code gets the setting
	// permissions.blockReadsOutsideWorkingDirectories. Codex has no such
	// control, so its driver fails at the start.
	BlockReadsOutside bool
	// BlockWritesOutside refuses an edit of a file tool outside the
	// roots. The harness denies the call. Claude Code gets the setting
	// permissions.blockWritesOutsideWorkingDirectories. Codex in
	// [Bypass] gets the workspace-write sandbox.
	BlockWritesOutside bool
	// Fence runs the commands of the agent in an OS sandbox. The harness
	// uses the fence of its default sandbox, which runs only on macOS.
	// Claude Code gets the setting sandbox.enabled. Codex in [Bypass]
	// gets the workspace-write sandbox.
	Fence bool
}

// Validate returns an error for an unknown mode and for each rule that
// does not parse. The error names each bad rule.
func (p Policy) Validate() error {
	_, modeErr := ParseMode(string(p.Mode))
	_, rulesErr := p.Rules.parse()
	return errors.Join(modeErr, rulesErr)
}

// Decision is what a [Policy] says about one tool call.
type Decision struct {
	// Match is [AllowMatch], [AskMatch], or [DenyMatch]. The mode decides
	// each call that no rule matches, so a policy never gives [NoMatch].
	Match Match
	// Reason says why the policy denies the call. It is empty for each
	// other match.
	Reason string
	// Safety marks an ask about an edit of a protected path. An Allow
	// rule does not approve such an edit.
	Safety bool
}

// Decide returns what the policy says about the call c. Decide reads
// the fields of the policy, the symlinks of the roots, and the symlinks
// of each path of c at each call.
//
// If the policy is not valid, or Dir is empty and the working
// directory is unknown, Decide denies each call with the error. Call
// [Policy.Validate] first to get the error.
func (p Policy) Decide(c ai.ToolCall) Decision {
	d, err := p.compile()
	if err != nil {
		return deny(err.Error())
	}
	return d.decide(c)
}

// DenyReason is the [Decision.Reason] of a call of the tool name that
// the Deny rule blocks. A caller that checks [Rules] gives it, so a
// Deny rule gives one text on each path.
func DenyReason(rule, name string) string {
	return fmt.Sprintf("the rule %q denies the tool call %q", rule, name)
}

func deny(reason string) Decision {
	return Decision{
		Match:  DenyMatch,
		Reason: reason,
	}
}

// decider is a policy with its rules parsed and its roots resolved.
type decider struct {
	matcher
	mode   Mode
	roots  rootSet
	limits Limits
}

func (p Policy) compile() (decider, error) {
	mode, modeErr := ParseMode(string(p.Mode))
	m, err := p.Rules.compile(p.Dir)
	if err := errors.Join(modeErr, err); err != nil {
		return decider{}, err
	}

	roots := p.Roots
	if len(roots) == 0 {
		roots = []string{m.dir}
	}
	d := decider{
		matcher: m,
		mode:    mode,
		roots:   newRootSet(roots, m.dir),
		limits:  p.Limits,
	}
	return d, nil
}

func (d decider) decide(c ai.ToolCall) Decision {
	in := decodeInput(c.Input)
	targets, resolved := d.targets(c.Name, in)
	if !resolved {
		reason := fmt.Sprintf(
			"the tool call %q has a path with symlinks that do not resolve. Give the real path",
			c.Name,
		)
		return deny(reason)
	}

	match, text := d.match(c.Name, in)
	if match == DenyMatch {
		return deny(DenyReason(text, c.Name))
	}
	kind := kindOf(c.Name)
	if reason := d.outsideLimit(c.Name, kind, targets); reason != "" {
		return deny(reason)
	}
	ask := Decision{Match: AskMatch}
	if match == AskMatch {
		return ask
	}
	command := d.readOnly(c.Name, kind, in)
	if d.limits.BlockReadsOutside && command == readsOutside {
		return ask
	}
	// An Allow rule does not approve an edit of a protected path.
	editsProtected := kind == tool.KindEdit && protected(targets)
	if editsProtected && d.mode != Bypass {
		return Decision{
			Match:  AskMatch,
			Safety: true,
		}
	}
	run := Decision{Match: AllowMatch}
	if match == AllowMatch {
		return run
	}

	inRoots := d.inRoots(targets)
	switch {
	case d.mode == Bypass:
		return run
	case kind == tool.KindRead && inRoots:
		return run
	case kind == tool.KindRead:
		return ask
	case command == readsInside:
		return run
	case command == readsOutside:
		return ask
	case d.mode == AcceptEdits && kind == tool.KindEdit && inRoots:
		return run
	}
	return ask
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
	for line := range strings.SplitSeq(in.Command, "\n") {
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

// targets returns the spellings of each path of a call of a file tool.
// A call of another tool has no targets. ok is false if the symlinks of
// a path do not resolve.
func (pl place) targets(name string, in toolInput) (targets [][]string, ok bool) {
	if contentOf(name) != tool.ContentPath || !in.ok {
		return nil, true
	}
	for _, p := range in.paths(name) {
		spellings, ok := spellingsOf(resolvePath(p, pl.dir, pl.home))
		if !ok {
			return nil, false
		}
		targets = append(targets, spellings)
	}
	return targets, true
}

// readOnlyResult is what [decider.readOnly] finds in a bash command.
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
func (d decider) readOnly(name string, kind tool.Kind, in toolInput) readOnlyResult {
	if name != tool.Bash || kind != tool.KindExec || !in.ok {
		return notReadOnly
	}
	paths, ok := readOnlyPaths(in.Command, d.dir, d.home)
	if !ok {
		return notReadOnly
	}
	for _, p := range paths {
		spellings, ok := spellingsOf(p)
		if !ok || !d.roots.contains(spellings) {
			return readsOutside
		}
	}
	return readsInside
}

// inRoots reports if each spelling of each target is in a root.
func (d decider) inRoots(targets [][]string) bool {
	for _, spellings := range targets {
		if !d.roots.contains(spellings) {
			return false
		}
	}
	return true
}

// outsideLimit returns the reason of a deny if a hard limit of the
// policy blocks a call of the kind with the targets. It returns "" if
// no limit blocks the call.
func (d decider) outsideLimit(name string, kind tool.Kind, targets [][]string) string {
	var limit string
	switch {
	case kind == tool.KindRead && d.limits.BlockReadsOutside:
		limit = "reads"
	case kind == tool.KindEdit && d.limits.BlockWritesOutside:
		limit = "writes"
	default:
		return ""
	}
	for _, spellings := range targets {
		if d.roots.contains(spellings) {
			continue
		}
		return fmt.Sprintf(
			"the tool call %q is denied: the path %q is outside the roots of the session, and the policy blocks %s outside them",
			name,
			spellings[0],
			limit,
		)
	}
	return ""
}

// EditsProtected reports if the call c edits a protected path, as
// [Policy.Decide] finds it. The package doc gives the protected paths.
// dir is the folder of a relative path. A driver whose agent asks on its
// own, such as Claude Code, uses it to mark a request for approval as a
// safety check.
func EditsProtected(c ai.ToolCall, dir string) bool {
	if kindOf(c.Name) != tool.KindEdit {
		return false
	}
	pl, err := newPlace(dir)
	if err != nil {
		return false
	}
	targets, _ := pl.targets(c.Name, decodeInput(c.Input))
	return protected(targets)
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

// firstMatch returns the first Deny rule or Ask rule in rules that
// matches the call, or nil. If the input does not decode, a rule with
// content matches, so a bad input cannot pass a Deny rule.
func (pl place) firstMatch(rules []rule, name string, in toolInput) *rule {
	for i := range rules {
		r := &rules[i]
		if !r.matchesName(name) {
			continue
		}
		if !r.hasContent || !in.ok {
			return r
		}
		if pl.contentMatches(r, name, in) {
			return r
		}
	}
	return nil
}

// contentMatches reports if the content of r matches the input of the
// call. A path rule matches a call with several paths if it matches one
// spelling of one path, and a call with no path. So a Deny rule and an
// Ask rule cannot miss a path, a symlink, or a change of letter case.
// [matcher.allowed] needs each spelling of each path instead. A domain
// rule matches a URL with no host, for the same reason.
func (pl place) contentMatches(r *rule, name string, in toolInput) bool {
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
		spellings, _ := spellingsOf(resolvePath(p, pl.dir, pl.home))
		for _, s := range spellings {
			for _, f := range forms(s) {
				if r.path.matches(f, pl.dir, pl.home, true) {
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
func (pl place) eachPathMatches(r *rule, name string, in toolInput) bool {
	paths := in.paths(name)
	if len(paths) == 0 {
		return false
	}
	for _, p := range paths {
		spellings, ok := spellingsOf(resolvePath(p, pl.dir, pl.home))
		if !ok {
			return false
		}
		for _, s := range spellings {
			matches := slices.ContainsFunc(forms(s), func(f string) bool {
				return r.path.matches(f, pl.dir, pl.home, false)
			})
			if !matches {
				return false
			}
		}
	}
	return true
}

// allowed reports if the Allow rules match the call. For the bash
// tool, the Allow rules together must match each part of the command.
// If the input does not decode, only a rule without content matches.
func (m matcher) allowed(name string, in toolInput) bool {
	var commands []shellRule
	for i := range m.allow {
		r := &m.allow[i]
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
		if m.eachPathMatches(r, name, in) {
			return true
		}
	}
	if len(commands) == 0 {
		return false
	}
	return commandAllowed(in.Command, commands)
}
