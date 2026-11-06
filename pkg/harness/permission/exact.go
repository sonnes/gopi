package permission

import (
	"os"
	"slices"
	"strings"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/tool"
)

const (
	// The Claude Code names of the rules on paths. A rule on Edit covers
	// each edit tool, and a rule on Read covers each read tool.
	editRuleName = "Edit"
	readRuleName = "Read"
	// globMeta are the characters that a doublestar glob reads as a
	// pattern.
	globMeta = `*?[]{}\`
)

// Exact returns the narrowest rule that allows the call c, with the
// Claude Code name of its tool. dir is the folder of a relative path.
//
//   - A bash call gives Bash with the exact command. Each * and each
//     backslash before a * or a backslash is escaped, so the rule
//     matches the command as text.
//   - A call of a file tool with one path gives Edit or Read with the
//     absolute path, as "Edit(//w/app/a.go)". Each glob character is
//     escaped. The rule also matches each path in the folder at that
//     path.
//   - A web_fetch call gives WebFetch with the host of its URL.
//   - A call of a tool that takes no content, such as an MCP tool,
//     gives the name of the tool.
//
// Exact reports false if no rule is that narrow: the input does not
// decode, a bash command is empty, a patch has more than one path, a
// path goes through a symlink, or a URL has no host. It also reports
// false for a tool of the caller whose name is a Claude Code name, such
// as "Read", because a rule on that name covers other tools. Exact
// checks its rule with [Rules.Check], so the rule never allows less
// than c.
func Exact(c ai.ToolCall, dir string) (string, bool) {
	text, ok := exactRule(c, dir)
	if !ok {
		return "", false
	}
	allow := Rules{Allow: []string{text}}
	m, _, err := allow.Check(c, dir)
	if err != nil || m != AllowMatch {
		return "", false
	}
	return text, true
}

// exactRule returns the rule of [Exact] before the check.
func exactRule(c ai.ToolCall, dir string) (string, bool) {
	k := known(c.Name)
	if k.Name == "" {
		return otherToolRule(c.Name)
	}
	in := decodeInput(c.Input)
	switch k.Content {
	case tool.ContentCommand:
		command := strings.TrimSpace(in.Command)
		if !in.ok || command == "" {
			return "", false
		}
		return withContent(ruleName(k), escapeCommand(command)), true
	case tool.ContentPath:
		paths := in.paths(c.Name)
		if !in.ok || len(paths) != 1 {
			return "", false
		}
		abs, err := absDir(dir)
		if err != nil {
			return "", false
		}
		home, _ := os.UserHomeDir()
		p := resolvePath(paths[0], abs, home)
		if p == string(os.PathSeparator) {
			return "", false
		}
		// The path is absolute, so one more slash makes it relative to
		// the root of the file system.
		return withContent(pathRuleName(k), "/"+escapeGlob(p)), true
	case tool.ContentDomain:
		host, ok := hostOf(in.URL)
		if !in.ok || !ok {
			return "", false
		}
		return withContent(ruleName(k), domainPrefix+host), true
	}
	return ruleName(k), true
}

// otherToolRule returns the rule for a tool that the registry does not
// hold, such as an MCP tool or a tool of the caller: its name. It
// reports false for a name that a rule reads as more than one tool.
func otherToolRule(name string) (string, bool) {
	_, isClaudeName := tool.Lookup(name)
	isPrefix := strings.HasSuffix(name, namePrefixMark)
	if isClaudeName || isPrefix || isMCPServer(name) {
		return "", false
	}
	return name, true
}

// ruleName returns the Claude Code name of a well-known tool, or its
// gopi name if Claude Code does not have the tool.
func ruleName(k tool.Known) string {
	if n := k.ClaudeName(); n != "" {
		return n
	}
	return k.Name
}

// pathRuleName returns Edit for an edit tool that a rule on Edit
// covers, and Read for a read tool that a rule on Read covers. Each
// other file tool, such as notebook_edit, gets its own name.
func pathRuleName(k tool.Known) string {
	switch {
	case slices.Contains(tool.RuleNames(editRuleName), k.Name):
		return editRuleName
	case slices.Contains(tool.RuleNames(readRuleName), k.Name):
		return readRuleName
	}
	return ruleName(k)
}

// withContent returns the rule "name(content)", with the content
// escaped for [splitRule].
func withContent(name, content string) string {
	return name + "(" + escapeContent(content) + ")"
}

// escapeCommand escapes a command for the content of an exact bash
// rule, as [unescapeCommand] reads it: each * gets a backslash, and so
// does each backslash before a * or a backslash.
func escapeCommand(command string) string {
	var b strings.Builder
	for i := 0; i < len(command); i++ {
		c := command[i]
		next := byte(0)
		if i+1 < len(command) {
			next = command[i+1]
		}
		needsEscape := c == wildcard || (c == '\\' && (next == wildcard || next == '\\'))
		if needsEscape {
			b.WriteByte('\\')
		}
		b.WriteByte(c)
	}
	return b.String()
}

// escapeGlob escapes each character of p that a doublestar glob reads
// as a pattern.
func escapeGlob(p string) string {
	var b strings.Builder
	for _, c := range p {
		if strings.ContainsRune(globMeta, c) {
			b.WriteByte('\\')
		}
		b.WriteRune(c)
	}
	return b.String()
}

// escapeContent escapes the content of a rule, so that [splitRule]
// gives the content back. [unescapeContent] removes a backslash before
// a parenthesis, then halves each run of backslashes. So a run of n
// backslashes becomes 2n-1 backslashes before another character, and
// 2n before a parenthesis or the end. A parenthesis needs no escape,
// because the first unescaped "(" and the last unescaped ")" of a rule
// bound the content. The content then reads the same as it does in a
// settings file of Claude Code, where most rules have no escape.
func escapeContent(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			i++
			continue
		}
		end := i
		for end < len(s) && s[end] == '\\' {
			end++
		}
		n := end - i
		count := 2*n - 1
		beforeParen := end < len(s) && (s[end] == '(' || s[end] == ')')
		if end == len(s) || beforeParen {
			count = 2 * n
		}
		b.WriteString(strings.Repeat(`\`, count))
		i = end
	}
	return b.String()
}
