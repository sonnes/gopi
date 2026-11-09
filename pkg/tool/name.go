package tool

import (
	"slices"
	"strings"
)

// The gopi names of the well-known tools. Each driver of gopi gives
// these names to a caller, so a caller sees one name for one tool on
// each driver.
const (
	Bash            = "bash"
	ReadFile        = "read_file"
	EditFile        = "edit_file"
	WriteFile       = "write_file"
	Grep            = "grep"
	Find            = "find"
	ApplyPatch      = "apply_patch"
	WebSearch       = "web_search"
	WebFetch        = "web_fetch"
	TodoWrite       = "todo_write"
	AskUserQuestion = "ask_user_question"
	Skill           = "skill"
	Agent           = "agent"
	ToolSearch      = "tool_search"
)

// The gopi names of the tools that only Claude Code has. The registry
// holds them, so an agent file or a rule that names one of them reads
// the same on each driver.
const (
	NotebookEdit     = "notebook_edit"
	ExitPlanMode     = "exit_plan_mode"
	LSP              = "lsp"
	Monitor          = "monitor"
	TaskStop         = "task_stop"
	SendMessage      = "send_message"
	CronCreate       = "cron_create"
	CronDelete       = "cron_delete"
	CronList         = "cron_list"
	ScheduleWakeup   = "schedule_wakeup"
	EnterWorktree    = "enter_worktree"
	ExitWorktree     = "exit_worktree"
	PushNotification = "push_notification"
)

const (
	// MCPPrefix starts the name of each MCP tool, as in
	// "mcp__<server>__<tool>".
	MCPPrefix = "mcp__"
	// MCPSeparator divides the server and the tool in an MCP name.
	MCPSeparator = "__"
	// contentStart starts the content of a rule, as in "bash(ls *)".
	contentStart = "("
)

// Known is a well-known tool: its gopi name, its names in Claude Code,
// and the facts that a permission rule needs.
type Known struct {
	// Name is the gopi name, in snake case.
	Name string
	// Claude lists the names of the tool in Claude Code. The first is
	// the name of the current CLI, and the Claude Code driver writes
	// it. An older name, such as "Task" for agent, follows it. Claude
	// is nil for a tool that Claude Code does not have.
	Claude []string
	// RuleAliases are the Claude Code names whose permission rules also
	// cover the tool. A rule of Claude Code on Read also covers Grep and
	// Glob, so grep and find have the rule alias "Read". It is nil for
	// most tools.
	RuleAliases []string
	// Kind is what the tool can do. The default is [KindOther].
	Kind Kind
	// Content is what the content of a rule on the tool matches. The
	// default is [ContentNone].
	Content Content
}

// ClaudeName returns the name that the current Claude Code CLI gives
// the tool. It is empty for a tool that Claude Code does not have.
func (k Known) ClaudeName() string {
	if len(k.Claude) == 0 {
		return ""
	}
	return k.Claude[0]
}

// knownNames is the registry. Its order is the order of the names in
// the result of [RuleNames].
var knownNames = []Known{
	{
		Name:    Bash,
		Claude:  []string{"Bash"},
		Kind:    KindExec,
		Content: ContentCommand,
	},
	{
		Name:    ReadFile,
		Claude:  []string{"Read"},
		Kind:    KindRead,
		Content: ContentPath,
	},
	{
		Name:    EditFile,
		Claude:  []string{"Edit", "MultiEdit"},
		Kind:    KindEdit,
		Content: ContentPath,
	},
	{
		Name:        WriteFile,
		Claude:      []string{"Write"},
		RuleAliases: []string{"Edit"},
		Kind:        KindEdit,
		Content:     ContentPath,
	},
	{
		Name:        Grep,
		Claude:      []string{"Grep"},
		RuleAliases: []string{"Read"},
		Kind:        KindRead,
		Content:     ContentPath,
	},
	{
		Name:        Find,
		Claude:      []string{"Glob"},
		RuleAliases: []string{"Read"},
		Kind:        KindRead,
		Content:     ContentPath,
	},
	{
		// The content of an apply_patch rule is a glob for each path
		// that the patch changes.
		Name:        ApplyPatch,
		RuleAliases: []string{"Edit"},
		Kind:        KindEdit,
		Content:     ContentPath,
	},
	{
		Name:   WebSearch,
		Claude: []string{"WebSearch"},
	},
	{
		Name:    WebFetch,
		Claude:  []string{"WebFetch"},
		Content: ContentDomain,
	},
	{
		Name:   TodoWrite,
		Claude: []string{"TodoWrite"},
		Kind:   KindRead,
	},
	{
		Name:   AskUserQuestion,
		Claude: []string{"AskUserQuestion"},
		Kind:   KindRead,
	},
	{
		Name:   Skill,
		Claude: []string{"Skill"},
		Kind:   KindRead,
	},
	{
		Name:   Agent,
		Claude: []string{"Agent", "Task"},
		Kind:   KindRead,
	},
	{
		Name:   ToolSearch,
		Claude: []string{"ToolSearch"},
		Kind:   KindRead,
	},
	{
		Name:    NotebookEdit,
		Claude:  []string{"NotebookEdit"},
		Kind:    KindEdit,
		Content: ContentPath,
	},
	{
		Name:   ExitPlanMode,
		Claude: []string{"ExitPlanMode"},
		Kind:   KindRead,
	},
	{
		Name:   LSP,
		Claude: []string{"LSP"},
		Kind:   KindRead,
	},
	{Name: Monitor, Claude: []string{"Monitor"}},
	{Name: TaskStop, Claude: []string{"TaskStop"}},
	{Name: SendMessage, Claude: []string{"SendMessage"}},
	{Name: CronCreate, Claude: []string{"CronCreate"}},
	{Name: CronDelete, Claude: []string{"CronDelete"}},
	{Name: CronList, Claude: []string{"CronList"}},
	{Name: ScheduleWakeup, Claude: []string{"ScheduleWakeup"}},
	{Name: EnterWorktree, Claude: []string{"EnterWorktree"}},
	{Name: ExitWorktree, Claude: []string{"ExitWorktree"}},
	{Name: PushNotification, Claude: []string{"PushNotification"}},
}

// Lookup returns the well-known tool with the gopi name or the Claude
// Code name. It reports false for a name that the registry does not
// hold.
func Lookup(name string) (Known, bool) {
	for _, k := range knownNames {
		if k.Name == name || slices.Contains(k.Claude, name) {
			return k, true
		}
	}
	return Known{}, false
}

// Registry returns a copy of each well-known tool of the registry, in
// order.
func Registry() []Known {
	return slices.Clone(knownNames)
}

// MCPName returns the gopi name of the tool of an MCP server.
func MCPName(server, tool string) string {
	return MCPPrefix + server + MCPSeparator + tool
}

// Canonical returns the gopi name of a tool name. A Claude Code name of
// a well-known tool becomes its gopi name. Each other name stays the
// same, so an MCP name, a tool of the caller, and a tool of a CLI that
// the registry does not hold, such as ExitPlanMode, pass through. The
// match is exact, so "BASH" stays "BASH".
func Canonical(name string) string {
	if k, ok := Lookup(name); ok {
		return k.Name
	}
	return name
}

// RuleNames returns the tool names that a permission rule on name
// covers: the gopi name of each tool of its group, then name itself if
// it is not a gopi name. A rule of Claude Code on Read also covers Grep
// and Glob, so "Read" gives read_file, grep, find, and Read. The name
// itself stays, so a rule on "Read" also matches a tool of the caller
// with that name.
func RuleNames(name string) []string {
	var out []string
	for _, k := range knownNames {
		covers := k.Name == name ||
			slices.Contains(k.Claude, name) ||
			slices.Contains(k.RuleAliases, name)
		if covers {
			out = append(out, k.Name)
		}
	}
	if !slices.Contains(out, name) {
		out = append(out, name)
	}
	return out
}

// ListNames returns the tool names of a tools list, such as the "tools"
// key of an agent file: the name of each entry as the list writes it,
// then its gopi name if it differs. A list entry names one tool, so
// "Read" gives Read and read_file, and not grep. An entry can have
// content, such as "bash(git diff *)", and ListNames reads only the
// tool name.
func ListNames(list []string) []string {
	var out []string
	for _, entry := range list {
		name, _ := SplitRule(entry)
		for _, n := range []string{name, Canonical(name)} {
			if !slices.Contains(out, n) {
				out = append(out, n)
			}
		}
	}
	return out
}

// SplitRule returns the tool name of a permission rule, and the rest of
// the rule with its parentheses. The content is empty for a rule with
// no content. A tool name has no parenthesis, so the first one starts
// the content.
func SplitRule(rule string) (name, content string) {
	i := strings.Index(rule, contentStart)
	if i < 0 {
		return rule, ""
	}
	return rule[:i], rule[i:]
}

// WholeTools returns the rules of rules with no content, in order. Each
// one names a whole tool, such as "web_fetch". A driver removes the
// tool of such a Deny rule, so the model does not see it.
func WholeTools(rules []string) []string {
	var out []string
	for _, r := range rules {
		if _, content := SplitRule(r); content == "" {
			out = append(out, r)
		}
	}
	return out
}
