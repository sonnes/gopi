package harness

import (
	"context"

	"github.com/sonnes/gopi/pkg/harness/skill"
	"github.com/sonnes/gopi/pkg/harness/subagent"
	"github.com/sonnes/gopi/pkg/tool"
)

// SkillsReader reads skills. The harness calls ReadSkills each time
// that it assembles an agent, so an edit reaches the next agent. A slow
// reader must cache on its own. A [skill.Skill] is a SkillsReader, and
// so is a [files.SkillDir].
type SkillsReader interface {
	ReadSkills(ctx context.Context) ([]skill.Skill, error)
}

// AgentsReader reads subagents. The harness calls ReadAgents each time
// that it assembles an agent. A [subagent.Agent] is an AgentsReader,
// and so is a [files.AgentDir].
type AgentsReader interface {
	ReadAgents(ctx context.Context) ([]subagent.Agent, error)
}

// ToolsReader reads tools. The harness calls ReadTools each time that
// it assembles an agent. A reader that returns no tools adds nothing.
type ToolsReader interface {
	ReadTools(ctx context.Context) ([]tool.Tool, error)
}

// Tools returns a reader that gives tools as they are, in order.
func Tools(tools ...tool.Tool) ToolsReader {
	return toolList(tools)
}

// toolList is the reader that [Tools] returns.
type toolList []tool.Tool

func (l toolList) ReadTools(context.Context) ([]tool.Tool, error) {
	return l, nil
}
