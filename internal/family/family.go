// Package family matches a model ID to its family. A family is a prefix
// of the model IDs of one model line, such as "gpt-6". A provider keys
// its table of model features by family, so a later model of a known
// family gets the features of the family with no change to the code.
//
// The reasons are in docs/concepts/ai/provider.md.
package family

import "strings"

// separators are the characters that can follow a family in a model
// ID: "gpt-6" is the family of "gpt-6-luna" and of "gpt-6.1-sol", and
// not of "gpt-60".
const separators = "-."

// Of returns the value of the family of id in table. If more than one
// family matches, the longest one wins, so a table can give a later
// model of a family other features. The bool is false if no family
// matches.
func Of[V any](table map[string]V, id string) (V, bool) {
	var value V
	best := ""
	for family, v := range table {
		if len(family) > len(best) && inFamily(id, family) {
			best = family
			value = v
		}
	}
	return value, best != ""
}

// inFamily reports if id is family, or starts with family and a
// separator.
func inFamily(id, family string) bool {
	rest, ok := strings.CutPrefix(id, family)
	if !ok {
		return false
	}
	return rest == "" || strings.ContainsRune(separators, rune(rest[0]))
}

// gpt is the GPT families that take a developer message in the
// conversation, tool changes in place, and a cache breakpoint. The list
// comes from the OpenAI docs.
var gpt = map[string]bool{
	"gpt-6":   true,
	"gpt-5.6": true,
}

// GPT reports if id is a model of a GPT family. The openai and
// openaichat providers both read it, so the two keep one list.
func GPT(id string) bool {
	_, ok := Of(gpt, id)
	return ok
}
