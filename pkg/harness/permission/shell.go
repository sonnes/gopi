package permission

import (
	"regexp"
	"strings"
)

// shellKind is the kind of match of a command rule.
type shellKind int

const (
	// shellExact matches the whole command.
	shellExact shellKind = iota
	// shellPrefix matches a command that starts with the prefix, at a
	// word boundary.
	shellPrefix
	// shellWildcard matches a pattern in which * is any characters.
	shellWildcard
)

const (
	// legacyPrefixMark ends a prefix rule, as in "git push:*".
	legacyPrefixMark = ":*"
	// wildcard is any characters in a command pattern.
	wildcard = '*'
	// wildcardRegexp is the regular expression of a wildcard.
	wildcardRegexp = ".*"
	// optionalArgsRegexp replaces a trailing " *": the arguments are
	// optional.
	optionalArgsRegexp = "( .*)?"
)

// shellRule is the content of a rule for the bash tool. The three
// kinds and their match rules come from shellRuleMatching.ts in
// utils/permissions of Claude Code.
type shellRule struct {
	kind shellKind
	// text is the command of an exact rule, or the prefix of a prefix
	// rule.
	text string
	// re is the pattern of a wildcard rule.
	re *regexp.Regexp
}

// parseShellRule parses the content of a bash rule. A content that ends
// in ":*" is a prefix rule. A content with a * that no backslash escapes
// is a wildcard rule. Each other content is an exact rule.
func parseShellRule(content string) shellRule {
	prefix, isPrefix := strings.CutSuffix(content, legacyPrefixMark)
	if isPrefix && prefix != "" {
		return shellRule{
			kind: shellPrefix,
			text: prefix,
		}
	}
	if hasWildcard(content) {
		return shellRule{
			kind: shellWildcard,
			re:   compileWildcard(content),
		}
	}
	return shellRule{
		kind: shellExact,
		text: content,
	}
}

// hasWildcard reports if pattern has a * that no backslash escapes.
func hasWildcard(pattern string) bool {
	for i := 0; i < len(pattern); i++ {
		if pattern[i] == wildcard && !isEscaped(pattern, i) {
			return true
		}
	}
	return false
}

// compileWildcard turns a pattern into a regular expression that
// matches the whole command. \* is a literal *, and \\ is a literal
// backslash. If the only * of the pattern is a trailing " *", the
// space and the arguments are optional, so "git *" matches "git" and
// "git add". The rules come from matchWildcardPattern in Claude Code.
func compileWildcard(pattern string) *regexp.Regexp {
	pattern = strings.TrimSpace(pattern)

	var b strings.Builder
	stars := 0
	endsInSpaceStar := false
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		hasNext := i+1 < len(pattern)
		escapesNext := hasNext && (pattern[i+1] == wildcard || pattern[i+1] == '\\')
		isEscape := c == '\\' && escapesNext
		if isEscape {
			b.WriteString(regexp.QuoteMeta(pattern[i+1 : i+2]))
			i++
			endsInSpaceStar = false
			continue
		}
		if c == wildcard {
			stars++
			endsInSpaceStar = i > 0 && pattern[i-1] == ' ' && !hasNext
			b.WriteString(wildcardRegexp)
			continue
		}
		b.WriteString(regexp.QuoteMeta(string(c)))
		endsInSpaceStar = false
	}

	expr := b.String()
	optionalArgs := endsInSpaceStar && stars == 1
	if optionalArgs {
		withoutTail := strings.TrimSuffix(expr, " "+wildcardRegexp)
		expr = withoutTail + optionalArgsRegexp
	}
	// The s flag makes . match a newline, so a * matches a command of
	// more than one line.
	return regexp.MustCompile(`(?s)^` + expr + `$`)
}

// matches reports if the rule matches one command, with no split.
func (r shellRule) matches(command string) bool {
	switch r.kind {
	case shellPrefix:
		return command == r.text || strings.HasPrefix(command, r.text+" ")
	case shellWildcard:
		return r.re.MatchString(command)
	default:
		return command == r.text
	}
}

// commandMatches reports if a Deny rule or an Ask rule matches the
// command: the whole command, or one of its parts. So a compound
// command cannot hide a denied part.
func commandMatches(command string, r shellRule) bool {
	command = strings.TrimSpace(command)
	if r.matches(command) {
		return true
	}
	parts, _ := splitCommand(command)
	for _, part := range parts {
		if r.matches(part) {
			return true
		}
	}
	return false
}

// commandAllowed reports if the Allow rules match the command. An exact
// rule can match the whole command. Otherwise, a rule must match each
// part, and no part can have a substitution.
func commandAllowed(command string, rules []shellRule) bool {
	command = strings.TrimSpace(command)
	for _, r := range rules {
		if r.kind == shellExact && r.text == command {
			return true
		}
	}

	parts, substitution := splitCommand(command)
	if substitution || len(parts) == 0 {
		return false
	}
	for _, part := range parts {
		if !anyMatches(rules, part) {
			return false
		}
	}
	return true
}

func anyMatches(rules []shellRule, command string) bool {
	for _, r := range rules {
		if r.matches(command) {
			return true
		}
	}
	return false
}

// splitCommand splits a command at &&, ||, ;, |, |&, &, and a newline
// outside quotes. A backslash escapes the next character outside single
// quotes. A & next to a > or a < is a redirection, as in 2>&1, and
// does not split. substitution is true if the command has $(...),
// backticks, <(...), or >(...) outside single quotes.
//
// The split reads text, not the grammar of the shell. It does not see
// a subshell, a heredoc, or a function as parts.
func splitCommand(command string) (parts []string, substitution bool) {
	var cur strings.Builder
	flush := func() {
		part := strings.TrimSpace(cur.String())
		if part != "" {
			parts = append(parts, part)
		}
		cur.Reset()
	}

	inSingle := false
	inDouble := false
	for i := 0; i < len(command); i++ {
		c := command[i]
		var next byte
		if i+1 < len(command) {
			next = command[i+1]
		}

		if inSingle {
			inSingle = c != '\''
			cur.WriteByte(c)
			continue
		}
		if c == '\\' && next != 0 {
			cur.WriteByte(c)
			cur.WriteByte(next)
			i++
			continue
		}

		startsSubstitution := c == '`' || (c == '$' && next == '(')
		if startsSubstitution {
			substitution = true
		}
		if inDouble {
			inDouble = c != '"'
			cur.WriteByte(c)
			continue
		}

		isProcessSubstitution := (c == '<' || c == '>') && next == '('
		if isProcessSubstitution {
			substitution = true
		}

		switch {
		case c == '\'':
			inSingle = true
		case c == '"':
			inDouble = true
		case isSeparator(command, i):
			flush()
			isDouble := (c == '&' || c == '|') && next == c
			isPipeAll := c == '|' && next == '&'
			if isDouble || isPipeAll {
				i++
			}
			continue
		}
		cur.WriteByte(c)
	}
	flush()
	return parts, substitution
}

// isSeparator reports if the byte at index i of command ends a part.
func isSeparator(command string, i int) bool {
	c := command[i]
	switch c {
	case ';', '\n', '|':
		return true
	case '&':
		afterRedirect := i > 0 && (command[i-1] == '>' || command[i-1] == '<')
		beforeRedirect := i+1 < len(command) && command[i+1] == '>'
		return !afterRedirect && !beforeRedirect
	}
	return false
}
