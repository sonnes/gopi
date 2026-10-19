package harness_test

import (
	"context"
	"fmt"
	"os"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/catalog"
	"github.com/sonnes/gopi/pkg/harness"
	"github.com/sonnes/gopi/pkg/harness/permission"
	"github.com/sonnes/gopi/pkg/harness/skill"
	"github.com/sonnes/gopi/pkg/harness/subagent"
	"github.com/sonnes/gopi/pkg/session"
)

// done is a provider that answers each call with "Done.".
type done struct{}

func (done) StreamText(context.Context, ai.Model, []ai.Message, ai.Options) *ai.EventStream {
	return ai.NewEventStream(func(push func(ai.Event)) (*ai.Message, error) {
		m := ai.AssistantMessage(ai.Text{Text: "Done."})
		m.StopReason = ai.StopReasonStop
		return &m, nil
	})
}

// A harness assembles a coding agent from options. Durable reads the
// files of the project on each call, and the session keeps its model.
func Example() {
	ctx := context.Background()
	cat := catalog.New()
	cat.RegisterTextProvider("fake", done{}, ai.Model{ID: "one"})
	dir, _ := os.MkdirTemp("", "project")
	defer os.RemoveAll(dir)

	h := harness.New(
		harness.Catalog(cat),
		harness.Model("fake/one"),
		harness.Dir(dir),
		harness.Sources(harness.Project),
		harness.Builtin(harness.NoBuiltins),
		harness.Skillset(skill.Skill{
			Name:        "deploy",
			Description: "Deploy the service.",
			Body:        "Run make deploy.",
		}),
		harness.Agents(subagent.Agent{
			Name:        "reviewer",
			Description: "Review a diff.",
			Prompt:      "You review code.",
		}),
		harness.Mode(permission.AcceptEdits),
		harness.Deny("bash(git push *)"),
	)

	a, err := h.Durable(ctx, "fix-login")
	if err != nil {
		panic(err)
	}
	msgs, err := a.Run(ctx, session.Text("Fix the login test.")).Wait()
	if err != nil {
		panic(err)
	}

	for _, info := range a.State().Tools() {
		fmt.Println("tool:", info.Name)
	}
	fmt.Println(msgs[len(msgs)-1].Text())
	// Output:
	// tool: skill
	// tool: task
	// Done.
}
