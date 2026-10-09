package agent

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/tool"
)

// searchToolName is the name of the search tool.
const searchToolName = "tool_search"

// defaultMaxResults is the number of tools that a keyword query loads
// when the call gives no max_results.
const defaultMaxResults = 5

// selectPrefix starts a query that names the tools to load.
const selectPrefix = "select:"

// nameWeight is the score of a query word in the name of a tool. A
// word of the name tells more about a tool than a word of its text.
const nameWeight = 2

const searchToolDescription = `Load deferred tools, so that you can call them. You know a deferred tool only by its name until this tool loads it. A call to a deferred tool that is not loaded fails.

Query forms:
- "select:get_weather,stock_price" loads the tools with these exact names.
- "weather forecast" loads the best keyword matches, up to max_results.
- "+stock price" loads only the tools with "stock" in the name, ranked by "price".

You can call a loaded tool from your next response.

The deferred tools are:`

// searchTool is the tool that loads deferred tools. An agent with a
// deferred tool adds it after the tools of [Toolset]. Its description
// names each deferred tool, so the model can load a tool by its name.
// A keyword query loads the tools whose words match the query best.
//
// The added tools arrive in a system message after the tool result, so
// the tools of the first call stay the same, and a provider can cache
// them. The history keeps the change, so a resumed history keeps the
// tools.
type searchTool struct {
	agent *Agent
	tools []tool.Tool
	// docs holds the words of each tool, in the order of tools.
	docs []doc
	// description is the description of the search, with the names of
	// tools. Toolset is fixed at New, so the description never changes,
	// and a provider caches it.
	description string
}

// doc is the words of one tool that a keyword query matches.
type doc struct {
	name []string
	text []string
}

// newSearchTool returns the search tool of a over the deferred tools.
func newSearchTool(a *Agent, tools []tool.Tool) *searchTool {
	docs := make([]doc, len(tools))
	names := make([]string, len(tools))
	for i, t := range tools {
		info := t.Info()
		names[i] = info.Name
		docs[i] = doc{
			name: words(info.Name),
			text: words(toolText(info)),
		}
	}
	return &searchTool{
		agent:       a,
		tools:       tools,
		docs:        docs,
		description: searchToolDescription + " " + strings.Join(names, ", ") + ".",
	}
}

// Info returns the info of the search tool.
func (t *searchTool) Info() ai.ToolInfo {
	return ai.ToolInfo{
		Name:        searchToolName,
		Description: t.description,
		InputSchema: &jsonschema.Schema{
			Type: "object",
			Properties: map[string]*jsonschema.Schema{
				"query": {
					Type:        "string",
					Description: `"select:" and the names of the tools, or keywords that describe the tool that you need.`,
				},
				"max_results": {
					Type:        "integer",
					Description: fmt.Sprintf("The maximum number of tools that a keyword query loads. The default is %d.", defaultMaxResults),
				},
			},
			Required: []string{"query"},
		},
	}
}

type searchInput struct {
	Query      string `json:"query"`
	MaxResults int    `json:"max_results"`
}

// query is one query of the search, in one of two forms. A select
// query names the tools to load. A keyword query ranks the tools.
type query struct {
	selected bool
	// names are the tools that a select query loads.
	names []string
	// required are the words that the name of each match must have.
	required []string
	// words are the other words of a keyword query. They rank the
	// matches.
	words []string
}

// parseQuery returns the query of s. A word that starts with "+" is
// required.
func parseQuery(s string) query {
	s = strings.TrimSpace(s)
	if list, ok := strings.CutPrefix(s, selectPrefix); ok {
		q := query{selected: true}
		for name := range strings.SplitSeq(list, ",") {
			if name = strings.TrimSpace(name); name != "" {
				q.names = append(q.names, name)
			}
		}
		return q
	}
	var q query
	for _, field := range strings.Fields(s) {
		if w, ok := strings.CutPrefix(field, "+"); ok {
			q.required = append(q.required, words(w)...)
		} else {
			q.words = append(q.words, words(field)...)
		}
	}
	return q
}

// empty reports if q gives nothing to search for.
func (q query) empty() bool {
	if q.selected {
		return len(q.names) == 0
	}
	return len(q.required) == 0 && len(q.words) == 0
}

// Run answers one call of the search tool. Every failure is an error
// result and no error, because an error stops the run of the agent, and
// the model can correct the call.
func (t *searchTool) Run(_ context.Context, call ai.ToolCall) (ai.ToolResult, error) {
	var in searchInput
	if err := json.Unmarshal(call.Input, &in); err != nil {
		return errorResult(fmt.Sprintf("invalid input: %s", err)), nil
	}
	q := parseQuery(in.Query)
	if q.empty() {
		return errorResult(`the query is empty. Give "select:" and tool names, or keywords.`), nil
	}

	var names, unknown []string
	if q.selected {
		names, unknown = t.selectNames(q.names)
	} else {
		limit := in.MaxResults
		if limit <= 0 {
			limit = defaultMaxResults
		}
		names = t.rank(q, limit)
	}
	t.agent.addTools(names)
	return textResult(loadedText(names, unknown, q.selected)), nil
}

// loadedText returns the text of a search result: the tools that it
// loaded, and the names of a select query that no deferred tool has.
func loadedText(names, unknown []string, selected bool) string {
	var parts []string
	switch {
	case len(names) == 1:
		parts = append(parts, "Loaded 1 tool. You can call it from your next response: "+names[0]+".")
	case len(names) > 1:
		parts = append(parts, fmt.Sprintf(
			"Loaded %d tools. You can call them from your next response: %s.",
			len(names),
			strings.Join(names, ", "),
		))
	case !selected:
		parts = append(parts, "No tool matches the query. Search again with other keywords.")
	}
	if len(unknown) > 0 {
		parts = append(parts, "No deferred tool is named "+strings.Join(unknown, ", ")+".")
	}
	return strings.Join(parts, " ")
}

// selectNames returns each name of a select query that is a deferred
// tool, once, and each name that is not.
func (t *searchTool) selectNames(names []string) (found, unknown []string) {
	for _, name := range names {
		isDeferred := slices.ContainsFunc(t.tools, func(d tool.Tool) bool {
			return d.Info().Name == name
		})
		switch {
		case !isDeferred:
			unknown = append(unknown, name)
		case !slices.Contains(found, name):
			found = append(found, name)
		}
	}
	return found, unknown
}

// rank returns the names of the tools that match q, best first, up to
// limit. A match has each required word of q in its name. With no
// required word, a match also needs a score above 0. Two tools with one
// score keep the order of Toolset.
func (t *searchTool) rank(q query, limit int) []string {
	type hit struct {
		index int
		score int
	}
	var hits []hit
	for i, d := range t.docs {
		if !matches(q.required, d) {
			continue
		}
		s := score(q.words, d)
		if s == 0 && len(q.required) == 0 {
			continue
		}
		hits = append(hits, hit{index: i, score: s})
	}
	slices.SortStableFunc(hits, func(x, y hit) int {
		return cmp.Compare(y.score, x.score)
	})

	var names []string
	for _, h := range hits[:min(limit, len(hits))] {
		names = append(names, t.tools[h.index].Info().Name)
	}
	return names
}

// matches reports if d has each required word in its name.
func matches(required []string, d doc) bool {
	for _, w := range required {
		if !slices.Contains(d.name, w) {
			return false
		}
	}
	return true
}

// score returns the score of d for words: [nameWeight] for each word
// in the name of d, and 1 for each other word in its text. Each word
// counts once, so a long description does not win by its length.
func score(words []string, d doc) int {
	total := 0
	for _, w := range words {
		switch {
		case slices.Contains(d.name, w):
			total += nameWeight
		case slices.Contains(d.text, w):
			total++
		}
	}
	return total
}

// toolText returns the text of a tool that a keyword query matches
// after its name: its description, and the names and the descriptions
// of its input properties.
func toolText(info ai.ToolInfo) string {
	parts := []string{info.Description}
	if info.InputSchema != nil {
		for name, prop := range info.InputSchema.Properties {
			parts = append(parts, name)
			if prop != nil {
				parts = append(parts, prop.Description)
			}
		}
	}
	return strings.Join(parts, " ")
}

// words splits s into lowercase words. It splits on every rune that is
// not a letter or a digit, and before an upper case letter that follows
// a lower case letter, so get_weather and getWeather both give "get"
// and "weather".
func words(s string) []string {
	var (
		out  []string
		word []rune
		prev rune
	)
	flush := func() {
		if len(word) > 0 {
			out = append(out, strings.ToLower(string(word)))
			word = word[:0]
		}
	}
	for _, r := range s {
		isWordRune := unicode.IsLetter(r) || unicode.IsDigit(r)
		switch {
		case !isWordRune:
			flush()
		case unicode.IsUpper(r) && unicode.IsLower(prev):
			flush()
			word = append(word, r)
		default:
			word = append(word, r)
		}
		prev = r
	}
	flush()
	return out
}
