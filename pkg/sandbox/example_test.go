package sandbox_test

import (
	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/sandbox"
	"github.com/sonnes/gopi/pkg/sandbox/bash"
	"github.com/sonnes/gopi/pkg/sandbox/edit"
	"github.com/sonnes/gopi/pkg/sandbox/find"
	"github.com/sonnes/gopi/pkg/sandbox/grep"
	"github.com/sonnes/gopi/pkg/sandbox/read"
	"github.com/sonnes/gopi/pkg/sandbox/write"
	"github.com/sonnes/gopi/pkg/tool"
)

// Give an agent every tool on the current directory. The file tools stay
// inside the directory, and the commands run in the default fence.
func Example() {
	// lm is a model bound to a provider, such as the Anthropic provider.
	var lm ai.LanguageModel

	box, err := sandbox.Local(".")
	if err != nil {
		panic(err)
	}
	tools := []tool.Tool{
		read.New(box),
		write.New(box),
		edit.New(box),
		bash.New(box),
		grep.New(box),
		find.New(box),
	}
	a := agent.New(lm, agent.Toolset(tools...))
	_ = a
}
