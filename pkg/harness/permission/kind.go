package permission

import (
	"github.com/sonnes/gopi/pkg/sandbox/bash"
	"github.com/sonnes/gopi/pkg/sandbox/edit"
	"github.com/sonnes/gopi/pkg/sandbox/find"
	"github.com/sonnes/gopi/pkg/sandbox/grep"
	"github.com/sonnes/gopi/pkg/sandbox/read"
	"github.com/sonnes/gopi/pkg/sandbox/write"
	"github.com/sonnes/gopi/pkg/tool/askquestion"
	"github.com/sonnes/gopi/pkg/tool/todowrite"
)

// Kind is what a tool can do. The [Mode] decides by the kind of the
// tool for a call that no rule matches.
type Kind int

const (
	// Other is a tool with an unknown effect, such as an MCP tool or a
	// custom tool. It asks in [Default] and [AcceptEdits], is blocked in
	// [Plan], and runs in [Bypass]. Other is the zero value, so a
	// missing kind gives the least access.
	Other Kind = iota
	// Read is a tool that reads and does not change the workspace.
	Read
	// Edit is a tool that changes files.
	Edit
	// Exec is a tool that runs commands.
	Exec
)

// The names of the built-in tools that have no package with a Name
// constant.
const (
	skillTool      = "skill"
	toolSearchTool = "tool_search"
	agentTool      = "agent"
)

// builtinKinds gives the kind of each built-in tool. A tool that is
// not in the map is [Other].
var builtinKinds = map[string]Kind{
	read.Name:        Read,
	grep.Name:        Read,
	find.Name:        Read,
	todowrite.Name:   Read,
	askquestion.Name: Read,
	skillTool:        Read,
	toolSearchTool:   Read,
	agentTool:        Read,
	write.Name:       Edit,
	edit.Name:        Edit,
	bash.Name:        Exec,
}

// kindOf returns the kind of the tool with the name. An entry in
// overrides wins over the built-in kind.
func kindOf(name string, overrides map[string]Kind) Kind {
	k, ok := overrides[name]
	if ok {
		return k
	}
	return builtinKinds[name]
}
