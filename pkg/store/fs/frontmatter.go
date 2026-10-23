package fs

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"go.yaml.in/yaml/v4"
)

// fence is the line that opens and closes the frontmatter of a file.
const fence = "---"

// star is the tools value that gives all the tools of the parent.
const star = "*"

var (
	errNoFrontmatter   = errors.New("the file does not start with a --- line")
	errOpenFrontmatter = errors.New("the frontmatter has no closing --- line")
)

var (
	// simpleLine matches a frontmatter line of one key and one value on
	// the same line. The pattern comes from the frontmatter parser of
	// Claude Code.
	simpleLine = regexp.MustCompile(`^([a-zA-Z_-]+):\s+(.+)$`)
	// specialValue matches a value that YAML reads as more than plain
	// text, such as a flow list, an alias, a comment, or a mapping.
	specialValue = regexp.MustCompile("[{}\\[\\]*&#!|>%@`]|: ")
)

// fields is the parsed frontmatter of a file. Each read method removes
// its key, so the keys that stay are the keys that no field takes.
type fields map[string]any

// parseFile reads a file of YAML frontmatter between two --- lines,
// then a body. It reads Windows line ends as Unix line ends. It returns
// the trimmed body.
func parseFile(data []byte) (fields, string, error) {
	meta, body, err := splitFrontmatter(data)
	if err != nil {
		return nil, "", err
	}

	f, err := parseYAML(meta)
	if err != nil {
		return nil, "", err
	}
	return f, strings.TrimSpace(body), nil
}

// splitFrontmatter returns the text between the opening and the closing
// --- lines, and the text after them.
func splitFrontmatter(data []byte) (string, string, error) {
	text := string(bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n")))

	first, rest, _ := strings.Cut(text, "\n")
	if strings.TrimSpace(first) != fence {
		return "", "", errNoFrontmatter
	}

	var meta strings.Builder
	for {
		line, after, found := strings.Cut(rest, "\n")
		if strings.TrimSpace(line) == fence {
			return meta.String(), after, nil
		}
		if !found {
			return "", "", errOpenFrontmatter
		}
		meta.WriteString(line)
		meta.WriteString("\n")
		rest = after
	}
}

// parseYAML parses the frontmatter. If the YAML is not valid, it tries
// one more time with [quoteValues], because an author often writes a
// value such as "Use when: the user asks" without quotes. Claude Code
// and Codex read such a file, so gopi reads it too.
//
// It decodes into a plain map and not into [fields], because the decoder
// gives each nested map the type of the outer map.
func parseYAML(meta string) (fields, error) {
	var m map[string]any
	err := yaml.Unmarshal([]byte(meta), &m)
	if err == nil {
		return fields(m), nil
	}

	m = nil
	if err := yaml.Unmarshal([]byte(quoteValues(meta)), &m); err != nil {
		return nil, fmt.Errorf("the frontmatter is not valid YAML: %w", err)
	}
	return fields(m), nil
}

// quoteValues puts double quotes around the value of each simple
// "key: value" line that holds a special YAML character. It does not
// change a value that has quotes already.
func quoteValues(meta string) string {
	lines := strings.Split(meta, "\n")
	for i, line := range lines {
		m := simpleLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		key := m[1]
		value := m[2]
		if isQuoted(value) || !specialValue.MatchString(value) {
			continue
		}

		escaped := strings.ReplaceAll(value, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `"`, `\"`)
		lines[i] = key + `: "` + escaped + `"`
	}
	return strings.Join(lines, "\n")
}

// isQuoted reports whether value starts and ends with the same quote.
func isQuoted(value string) bool {
	if len(value) < 2 {
		return false
	}
	first := value[0]
	last := value[len(value)-1]
	isQuote := first == '"' || first == '\''
	return isQuote && first == last
}

// text removes key and returns its value as trimmed text. A missing or
// empty key gives "". A number or a boolean gives its text form. A list
// or a map is an error.
func (f fields) text(key string) (string, error) {
	v := f[key]
	delete(f, key)
	return scalar(key, v)
}

// scalar returns v as trimmed text. See [fields.text].
func scalar(key string, v any) (string, error) {
	switch v := v.(type) {
	case nil:
		return "", nil
	case string:
		return strings.TrimSpace(v), nil
	case bool, int, int64, uint64, float64:
		return fmt.Sprint(v), nil
	default:
		return "", fmt.Errorf("the value of %q is not text", key)
	}
}

// flag removes key and returns its value as a boolean. It accepts true,
// false, and the strings "true" and "false" in any case. Any other
// value gives false, as in Claude Code.
func (f fields) flag(key string) bool {
	v := f[key]
	delete(f, key)
	switch v := v.(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	default:
		return false
	}
}

// count removes key and returns its value as a positive integer. It
// accepts a number or a string of digits. Any other value gives 0.
func (f fields) count(key string) int {
	v := f[key]
	delete(f, key)

	var n int
	switch v := v.(type) {
	case int:
		n = v
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return 0
		}
		n = parsed
	}
	return max(n, 0)
}

// list removes key and returns its value as a list of tools or names.
// The value is a YAML list, or a string that [splitList] splits. The
// boolean is false if the frontmatter has no key. A key with no value
// gives an empty list that is not nil.
func (f fields) list(key string) ([]string, bool, error) {
	v, ok := f[key]
	delete(f, key)
	if !ok {
		return nil, false, nil
	}

	out := []string{}
	switch v := v.(type) {
	case nil:
	case string:
		out = append(out, splitList(v)...)
	case []any:
		for _, item := range v {
			s, err := scalar(key, item)
			if err != nil {
				return nil, true, err
			}
			if s != "" {
				out = append(out, s)
			}
		}
	default:
		return nil, true, fmt.Errorf("the value of %q is not a list", key)
	}
	return out, true, nil
}

// splitList splits s on commas and white space. It does not split
// inside parentheses, so "Bash(git push *), Read" gives
// "Bash(git push *)" and "Read".
func splitList(s string) []string {
	var (
		out   []string
		item  strings.Builder
		depth int
	)
	flush := func() {
		if item.Len() > 0 {
			out = append(out, item.String())
			item.Reset()
		}
	}

	for _, r := range s {
		isDivider := depth == 0 && (r == ',' || unicode.IsSpace(r))
		switch {
		case isDivider:
			flush()
			continue
		case r == '(':
			depth++
		case r == ')' && depth > 0:
			depth--
		}
		item.WriteRune(r)
	}
	flush()
	return out
}

// meta returns the keys that no field took, or nil if there are none.
func (f fields) meta() map[string]any {
	if len(f) == 0 {
		return nil
	}
	return map[string]any(f)
}
