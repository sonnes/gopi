package def

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// slashPrefix starts the call of a skill in the text of a user.
const slashPrefix = "/"

// Slash reports whether text calls a skill of s, such as
// "/deploy now". A text calls a skill if it starts with "/", then the
// name or the qualified name of a skill, then a space or the end. A
// name has only ASCII letters, digits, and the characters "_:.-".
//
// Slash returns the name that the model sees for the skill, and the
// text after the name, with its space. A driver writes the name that
// the model sees, because the CLI knows the skill by that name. A user
// can then type the qualified name of each skill.
func (s Set) Slash(text string) (name, rest string, ok bool) {
	after, found := strings.CutPrefix(text, slashPrefix)
	if !found {
		return "", "", false
	}
	end := strings.IndexFunc(after, func(r rune) bool { return !slashNameRune(r) })
	if end == -1 {
		end = len(after)
	}
	typed, rest := after[:end], after[end:]
	if typed == "" {
		return "", "", false
	}
	if next, _ := utf8.DecodeRuneInString(rest); rest != "" && !unicode.IsSpace(next) {
		return "", "", false
	}
	i := s.skillIndex(typed)
	if i < 0 {
		return "", "", false
	}
	return s.SkillNames()[i], rest, true
}

// slashNameRune reports whether r can be in the name of a call of a
// skill.
func slashNameRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	}
	return strings.ContainsRune("_:.-", r)
}
