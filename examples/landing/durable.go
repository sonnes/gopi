// Package landing holds the code of the landing page of the site. The
// page shows the lines between a "// snippet:<name>" comment and its
// "// end" comment, without the error checks, so each preview shows the
// calls that create and customize an agent. The Go toolchain builds the
// package, so the page cannot show code that does not build. The
// functions are not called.
package landing

import (
	"context"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	storefs "github.com/sonnes/gopi/pkg/store/fs"
	"github.com/sonnes/gopi/pkg/tool"
)

// Triage is the preview of the hero: a durable agent with its tools
// and its prompt.
func Triage(
	ctx context.Context,
	model ai.LanguageModel,
	listIssues tool.Tool,
	readIssue tool.Tool,
) error {
	// snippet:durable
	store, err := storefs.Sessions("sessions")
	if err != nil {
		return err
	}

	a, err := agent.Durable(ctx, model, "triage",
		agent.Store(store),
		agent.Toolset(listIssues, readIssue),
		agent.System(agent.SystemText("Label each open issue.")),
		agent.Thinking(ai.ThinkingHigh),
	)
	if err != nil {
		return err
	}

	// With no input, the run continues from the last stored message.
	_, err = a.Run(ctx).Wait()
	// end
	return err
}
