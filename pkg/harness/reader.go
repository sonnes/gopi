package harness

import (
	"context"

	"github.com/sonnes/gopi/pkg/tool"
)

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
