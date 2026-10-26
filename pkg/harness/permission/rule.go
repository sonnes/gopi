package permission

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/sonnes/gopi/pkg/sandbox/bash"
	"github.com/sonnes/gopi/pkg/sandbox/edit"
	"github.com/sonnes/gopi/pkg/sandbox/find"
	"github.com/sonnes/gopi/pkg/sandbox/grep"
	"github.com/sonnes/gopi/pkg/sandbox/read"
	"github.com/sonnes/gopi/pkg/sandbox/write"
	"github.com/sonnes/gopi/pkg/tool/askquestion"
	"github.com/sonnes/gopi/pkg/tool/todowrite"
)

// aliases maps each Claude Code tool name to the gopi tools that it
// names in a rule. A rule of Claude Code, such as a disallowedTools
// list of an agent file, then works on the tools of gopi.
var aliases = map[string][]string{
	"Bash":            {bash.Name},
	"Read":            {read.Name, grep.Name, find.Name},
	"Edit":            {edit.Name, write.Name},
	"Write":           {write.Name},
	"Grep":            {grep.Name},
	"Glob":            {find.Name},
	"MultiEdit":       {edit.Name},
	"TodoWrite":       {todowrite.Name},
	"AskUserQuestion": {askquestion.Name},
	"Skill":           {skillTool},
	"Agent":           {agentTool},
}

// contentKind is what the content of a rule matches.
type contentKind int

const (
	// noContent is a tool that accepts no content in a rule.
	noContent contentKind = iota
	// commandContent is the command of the bash tool.
	commandContent
	// pathContent is the "path" input of a file tool.
	pathContent
)

// contentKinds gives the kind of content that each tool accepts. A
// tool that is not in the map accepts no content.
var contentKinds = map[string]contentKind{
	bash.Name:  commandContent,
	read.Name:  pathContent,
	write.Name: pathContent,
	edit.Name:  pathContent,
	grep.Name:  pathContent,
	find.Name:  pathContent,
}

const (
	// mcpPrefix starts the name of each MCP tool.
	mcpPrefix = "mcp__"
	// mcpSeparator divides the server and the tool in the name of an
	// MCP tool.
	mcpSeparator = "__"
	// namePrefixMark ends a tool name that is a prefix.
	namePrefixMark = "*"
	// wholeToolContent is content that matches each input, as no
	// content does.
	wholeToolContent = "*"
)

// rule is one parsed permission rule.
type rule struct {
	// text is the rule as the caller wrote it.
	text string
	// names are the tool names that the rule matches exactly.
	names []string
	// hasPrefix is true if the rule also matches each tool name that
	// starts with prefix.
	hasPrefix bool
	prefix    string
	// hasContent is true if the rule matches some inputs only.
	hasContent bool
	// command is the content for the bash tool.
	command shellRule
	// path is the content for a file tool.
	path pathRule
}

// matchesName reports if the rule names the tool.
func (r rule) matchesName(name string) bool {
	if r.hasPrefix && strings.HasPrefix(name, r.prefix) {
		return true
	}
	for _, n := range r.names {
		if n == name {
			return true
		}
	}
	return false
}

// parseRule parses a rule in the format "Tool" or "Tool(content)". The
// parser comes from permissionRuleValueFromString in
// utils/permissions/permissionRuleParser.ts of Claude Code: a backslash
// escapes a parenthesis in the content, and "Tool()" and "Tool(*)" are
// the same as "Tool". A malformed rule is an error here, where Claude
// Code reads it as a tool name.
func parseRule(text string) (rule, error) {
	r, err := parseRuleText(text)
	if err != nil {
		return rule{}, fmt.Errorf("permission: rule %q: %w", text, err)
	}
	return r, nil
}

func parseRuleText(text string) (rule, error) {
	name, content, err := splitRule(text)
	if err != nil {
		return rule{}, err
	}
	err = checkName(name)
	if err != nil {
		return rule{}, err
	}

	r := rule{text: text}
	isPrefix := strings.HasSuffix(name, namePrefixMark)
	switch {
	case isPrefix:
		r.hasPrefix = true
		r.prefix = strings.TrimSuffix(name, namePrefixMark)
	case isMCPServer(name):
		r.names = []string{name}
		r.hasPrefix = true
		r.prefix = name + mcpSeparator
	case aliases[name] != nil:
		r.names = aliases[name]
	default:
		r.names = []string{name}
	}

	if content == "" {
		return r, nil
	}
	if r.hasPrefix {
		return rule{}, fmt.Errorf("the tool name %q is a prefix, and takes no content", name)
	}

	r.hasContent = true
	switch contentKinds[r.names[0]] {
	case commandContent:
		r.command = parseShellRule(content)
	case pathContent:
		r.path, err = parsePathRule(content)
		if err != nil {
			return rule{}, err
		}
	default:
		return rule{}, fmt.Errorf("the tool %q takes no content", name)
	}
	return r, nil
}

// splitRule returns the tool name and the unescaped content of a rule.
// The content is empty if the rule has none.
func splitRule(text string) (name, content string, err error) {
	open := findUnescaped(text, '(', false)
	if open < 0 {
		return text, "", nil
	}
	end := findUnescaped(text, ')', true)
	if end < open {
		return "", "", fmt.Errorf("the content has no closing parenthesis")
	}
	if end != len(text)-1 {
		return "", "", fmt.Errorf("text follows the closing parenthesis")
	}

	name = text[:open]
	raw := text[open+1 : end]
	if raw == wholeToolContent {
		raw = ""
	}
	return name, unescapeContent(raw), nil
}

// checkName returns an error if name cannot be a tool name.
func checkName(name string) error {
	if name == "" {
		return fmt.Errorf("the rule has no tool name")
	}
	bad := strings.IndexFunc(name, func(c rune) bool {
		return unicode.IsSpace(c) || c == '(' || c == ')'
	})
	if bad >= 0 {
		return fmt.Errorf("the tool name %q has a space or a parenthesis", name)
	}
	star := strings.Index(name, namePrefixMark)
	starInside := star >= 0 && star != len(name)-1
	if starInside {
		return fmt.Errorf("the tool name %q has a * before its end", name)
	}
	return nil
}

// isMCPServer reports if name is "mcp__server", with no tool part.
func isMCPServer(name string) bool {
	server, ok := strings.CutPrefix(name, mcpPrefix)
	if !ok || server == "" {
		return false
	}
	return !strings.Contains(server, mcpSeparator)
}

// findUnescaped returns the index of the first c in s, or the last c
// if last is true, that no backslash escapes. It returns -1 if there is
// none. A character is escaped if an odd number of backslashes come
// before it.
func findUnescaped(s string, c byte, last bool) int {
	found := -1
	for i := 0; i < len(s); i++ {
		if s[i] != c || isEscaped(s, i) {
			continue
		}
		if !last {
			return i
		}
		found = i
	}
	return found
}

// isEscaped reports if an odd number of backslashes come before the
// byte at index i of s.
func isEscaped(s string, i int) bool {
	slashes := 0
	for j := i - 1; j >= 0 && s[j] == '\\'; j-- {
		slashes++
	}
	return slashes%2 == 1
}

// unescapeContent removes the escapes of the parentheses and of the
// backslashes, in the order of unescapeRuleContent of Claude Code.
func unescapeContent(s string) string {
	s = strings.ReplaceAll(s, `\(`, "(")
	s = strings.ReplaceAll(s, `\)`, ")")
	return strings.ReplaceAll(s, `\\`, `\`)
}
