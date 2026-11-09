package harness_test

import (
	"context"
	"fmt"
	"os"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/catalog"
	"github.com/sonnes/gopi/pkg/def"
	"github.com/sonnes/gopi/pkg/harness"
	"github.com/sonnes/gopi/pkg/permission"
	"github.com/sonnes/gopi/pkg/sandbox"
	"github.com/sonnes/gopi/pkg/sandbox/bash"
	"github.com/sonnes/gopi/pkg/sandbox/read"
	"github.com/sonnes/gopi/pkg/session"
	storefs "github.com/sonnes/gopi/pkg/store/fs"
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

// A harness assembles a coding agent from its options and the config of
// a session. Start reads the definitions of the folder on each call, and
// the session keeps its model.
func Example() {
	ctx := context.Background()
	cat := catalog.New()
	cat.RegisterTextProvider("fake", done{}, ai.Model{ID: "one"})
	dir, _ := os.MkdirTemp("", "project")
	defer os.RemoveAll(dir)
	box, err := sandbox.Local(dir)
	if err != nil {
		panic(err)
	}

	h := harness.New(
		harness.Catalog(cat),
		harness.Defs(def.Layers(
			storefs.Dir(dir),
			def.Set{Agents: []def.Agent{{
				Name:        "reviewer",
				Description: "Review a diff.",
				Prompt:      "You review code.",
			}}},
		)),
		harness.Toolset(harness.Tools(
			harness.Sandboxed(bash.New),
			harness.Sandboxed(read.New),
			harness.SkillTool(),
			harness.AgentTool(),
		)),
		harness.Sandbox(box),
	)

	r, err := h.Start(ctx, agent.Config{
		SessionID: "fix-login",
		Spec:      "fake/one",
		Dir:       dir,
		Defs: def.Set{Skills: []def.Skill{{
			Name:        "deploy",
			Description: "Deploy the service.",
			Body:        "Run make deploy.",
		}}},
		Mode: permission.AcceptEdits,
		Deny: []string{"bash(git push *)"},
	})
	if err != nil {
		panic(err)
	}
	a := r.(*agent.Agent)
	msgs, err := a.Run(ctx, session.Text("Fix the login test.")).Wait()
	if err != nil {
		panic(err)
	}

	for _, info := range a.State().Tools() {
		fmt.Println("tool:", info.Name)
	}
	fmt.Println(msgs[len(msgs)-1].Text())
	// Output:
	// tool: bash
	// tool: read_file
	// tool: skill
	// tool: agent
	// Done.
}
