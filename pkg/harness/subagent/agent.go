package subagent

import "context"

// Agent is the definition of a subagent. The main agent sees the name
// and the description, and it starts the subagent for a task. The
// subagent runs with the prompt as its system prompt.
//
// A reader of agent files sets the fields from the frontmatter. The doc
// comment of each field names its frontmatter key.
type Agent struct {
	// Name identifies the agent. The frontmatter key is "name". It is
	// required.
	Name string
	// Description tells the main agent when to start the agent. The
	// frontmatter key is "description". It is required. A reader
	// changes each "\n" escape in it to a newline.
	Description string
	// Tools lists the tools of the agent. The frontmatter key is
	// "tools". If Tools is nil, the agent gets all the tools of the
	// parent. If Tools is empty and not nil, the agent gets no tools. A
	// reader sets Tools to nil for "*" alone.
	Tools []string
	// Disallowed lists the tools that the agent cannot use, also when
	// Tools gives them. The frontmatter key is "disallowedTools". It is
	// nil by default.
	Disallowed []string
	// Model is the model of the agent, as the file writes it. The
	// frontmatter key is "model". It is "inherit", "", a bare model ID,
	// or a "provider/id" spec. If it is "" or "inherit", the agent uses
	// the model of the parent.
	Model string
	// Mode is the permission mode of the agent, as the file writes it.
	// The frontmatter key is "permissionMode". The harness maps it. If
	// it is empty, the agent uses the mode of the parent.
	Mode string
	// MaxTurns limits the number of turns of the agent. The frontmatter
	// key is "maxTurns". If it is 0, there is no limit. A reader sets 0
	// for a value that is not a positive integer.
	MaxTurns int
	// Skills names the skills that the agent loads at the start. The
	// frontmatter key is "skills". It is nil by default.
	Skills []string
	// Prompt is the system prompt of the agent: the text after the
	// frontmatter.
	Prompt string
	// Path is the location of the agent file, for a reader. It is empty
	// for an agent in code.
	Path string
	// Meta holds each other frontmatter key with its parsed value, such
	// as "mcpServers", "hooks", "memory", "color", "isolation",
	// "background", and "effort". It is nil if there are no other keys.
	Meta map[string]any
}

// ReadAgents returns a as the one agent of a reader. An Agent is then
// an agents reader, and an agent in code goes in the list of readers.
func (a Agent) ReadAgents(context.Context) ([]Agent, error) {
	return []Agent{a}, nil
}
