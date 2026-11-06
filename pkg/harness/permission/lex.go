package permission

import (
	"slices"
	"strings"
)

// word is one word of a simple command, with the quotes removed.
type word struct {
	text string
	// quoted is true if a part of the word is in quotes or follows a
	// backslash.
	quoted bool
	// glob is true if the word has a *, ?, or [ outside quotes.
	glob bool
	// tilde is true if the word starts with a ~ outside quotes, which is
	// the home folder.
	tilde bool
}

// The redirect operators that the lexer knows, longest first in each
// list. Each other operator, such as a heredoc, makes the command
// unknown.
var (
	outputOps = []string{"&>>", "&>", ">>", ">|", ">"}
	dupOps    = []string{">&", "<&"}
	inputOps  = []string{"<"}
	refused   = []string{"<<", "<>"}
)

// lexPart splits one simple command into its words, and returns the
// file of each input redirect. ok is false if the command has a part
// that the lexer cannot read: a $, a backtick, a parenthesis, a brace,
// a comment, a heredoc, a ~ of another user, or a redirect that writes
// to a file other than /dev/null.
func lexPart(text string) (words, inputs []word, ok bool) {
	l := lexer{s: text}
	for {
		l.skipSpace()
		if l.done() {
			return words, inputs, true
		}
		if l.atRedirect() {
			in, ok := l.redirect()
			if !ok {
				return nil, nil, false
			}
			if in != nil {
				inputs = append(inputs, *in)
			}
			continue
		}
		w, ok := l.word()
		if !ok {
			return nil, nil, false
		}
		isFD := !w.quoted && isDigits(w.text) && l.atRedirect()
		if isFD {
			continue
		}
		words = append(words, w)
	}
}

// lexer reads the words of one simple command.
type lexer struct {
	s string
	i int
}

func (l *lexer) done() bool { return l.i >= len(l.s) }

func (l *lexer) skipSpace() {
	for !l.done() && (l.s[l.i] == ' ' || l.s[l.i] == '\t') {
		l.i++
	}
}

// atRedirect reports if a redirect operator starts at the position.
func (l *lexer) atRedirect() bool {
	if l.done() {
		return false
	}
	c := l.s[l.i]
	return c == '<' || c == '>' || strings.HasPrefix(l.s[l.i:], "&>")
}

// redirect reads one redirect and its target. It returns the target of
// an input redirect, and nil for a redirect that writes to /dev/null or
// copies a file descriptor.
func (l *lexer) redirect() (*word, bool) {
	rest := l.s[l.i:]
	for _, op := range refused {
		if strings.HasPrefix(rest, op) {
			return nil, false
		}
	}
	op := ""
	for _, candidate := range slices.Concat(dupOps, outputOps, inputOps) {
		if strings.HasPrefix(rest, candidate) {
			op = candidate
			break
		}
	}
	if op == "" {
		return nil, false
	}
	l.i += len(op)
	l.skipSpace()
	if l.done() || l.atRedirect() {
		return nil, false
	}
	target, ok := l.word()
	if !ok {
		return nil, false
	}

	toDevNull := target.text == devNull && !target.glob && !target.tilde
	isDup := target.text == "-" || isDigits(target.text)
	switch {
	case op == ">&" && (isDup || toDevNull):
		return nil, true
	case op == "<&":
		return nil, isDup
	case op == "<":
		return &target, true
	}
	return nil, toDevNull
}

// word reads one word.
func (l *lexer) word() (word, bool) {
	var b strings.Builder
	var w word
	quotedGlob := false
	for !l.done() {
		c := l.s[l.i]
		switch {
		case c == ' ' || c == '\t' || c == '<' || c == '>':
			return l.finish(w, b.String(), quotedGlob)
		case c == '&':
			if strings.HasPrefix(l.s[l.i:], "&>") {
				return l.finish(w, b.String(), quotedGlob)
			}
			return word{}, false
		case c == '\'':
			end := strings.IndexByte(l.s[l.i+1:], '\'')
			if end < 0 {
				return word{}, false
			}
			quoted := l.s[l.i+1 : l.i+1+end]
			b.WriteString(quoted)
			w.quoted = true
			quotedGlob = quotedGlob || strings.ContainsAny(quoted, globChars)
			l.i += end + 2
		case c == '"':
			end := strings.IndexByte(l.s[l.i+1:], '"')
			if end < 0 {
				return word{}, false
			}
			quoted := l.s[l.i+1 : l.i+1+end]
			if strings.ContainsAny(quoted, "$`\\") {
				return word{}, false
			}
			b.WriteString(quoted)
			w.quoted = true
			quotedGlob = quotedGlob || strings.ContainsAny(quoted, globChars)
			l.i += end + 2
		case c == '\\':
			if l.i+1 >= len(l.s) {
				return word{}, false
			}
			next := l.s[l.i+1]
			b.WriteByte(next)
			w.quoted = true
			quotedGlob = quotedGlob || strings.IndexByte(globChars, next) >= 0
			l.i += 2
		case strings.IndexByte("$`(){};|", c) >= 0:
			return word{}, false
		case c == '#' && b.Len() == 0 && !w.quoted:
			return word{}, false
		case c == '~' && b.Len() == 0 && !w.quoted:
			atEnd := l.i+1 >= len(l.s) || strings.IndexByte(" \t/<>", l.s[l.i+1]) >= 0
			if !atEnd {
				return word{}, false
			}
			w.tilde = true
			b.WriteByte(c)
			l.i++
		default:
			if strings.IndexByte(globChars, c) >= 0 {
				w.glob = true
			}
			b.WriteByte(c)
			l.i++
		}
	}
	return l.finish(w, b.String(), quotedGlob)
}

// finish returns w with its text. A word with a glob character in quotes
// and one outside quotes is refused, because the glob would need the
// quoted character escaped. A ! is a word of its own, as find uses it.
func (l *lexer) finish(w word, text string, quotedGlob bool) (word, bool) {
	w.text = text
	if w.glob && (quotedGlob || strings.Contains(text, `\`)) {
		return word{}, false
	}
	bang := strings.Contains(text, "!") && !w.quoted && text != "!"
	if bang {
		return word{}, false
	}
	return w, true
}

// isDigits reports if s is one or more digits.
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
