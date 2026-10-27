package tool

// Kind is what a tool can do. A permission mode decides by the kind of
// a tool for a call that no rule matches.
type Kind int

const (
	// KindOther is a tool with an unknown effect, such as an MCP tool
	// or a tool of the caller. KindOther is the zero value, so a tool
	// with no kind gets the least access.
	KindOther Kind = iota
	// KindRead is a tool that reads and does not change the workspace.
	KindRead
	// KindEdit is a tool that changes files.
	KindEdit
	// KindExec is a tool that runs commands.
	KindExec
)

// Content is what the content of a permission rule on a tool matches,
// as in "bash(git push *)".
type Content int

const (
	// ContentNone is a tool whose rules take no content. ContentNone is
	// the zero value.
	ContentNone Content = iota
	// ContentCommand is the command of the bash tool.
	ContentCommand
	// ContentPath is the "path" input of a file tool, or each path that
	// a patch changes.
	ContentPath
)
