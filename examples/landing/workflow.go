package landing

import (
	"context"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
	"github.com/sonnes/gopi/pkg/tool"
)

// IssueToFix is the preview of the workflow band: two steps, each a
// durable agent with its own session. The answer of the first step is
// the input of the second.
func IssueToFix(
	ctx context.Context,
	model ai.LanguageModel,
	store session.Storer,
	readIssue tool.Tool,
	editFile tool.Tool,
) error {
	// snippet:workflow
	triage, err := agent.Durable(ctx, model, "issue-4812-triage",
		agent.Store(store),
		agent.Toolset(readIssue),
	)
	if err != nil {
		return err
	}
	plan, err := triage.Run(ctx, session.Text("Name the files that issue 4812 needs.")).Wait()
	if err != nil {
		return err
	}
	files := plan[len(plan)-1].Text()

	fix, err := agent.Durable(ctx, model, "issue-4812-fix",
		agent.Store(store),
		agent.Toolset(editFile),
	)
	if err != nil {
		return err
	}
	_, err = fix.Run(ctx, session.Text("Fix issue 4812 in these files: "+files)).Wait()
	// end
	return err
}
