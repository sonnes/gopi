package host_test

import (
	"context"
	"fmt"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/claude"
	"github.com/sonnes/gopi/pkg/catalog"
	"github.com/sonnes/gopi/pkg/harness"
	"github.com/sonnes/gopi/pkg/host"
	"github.com/sonnes/gopi/pkg/session"
)

// A host creates a session of Claude Code, and sends it a message. The
// same config with the model "anthropic/claude-opus-5-5" runs the model in
// the harness.
func ExampleHost_Create() {
	ctx := context.Background()
	store := session.NewMemoryStore() // a store that outlives the process
	repoDir := "."

	file, _ := catalog.ReadModelFile(ctx, "models.json")
	cat := catalog.New()
	cat.RegisterDriver("claude-code", claude.Driver(), file.Of("claude-code")...)
	cat.DefaultDriver(harness.New(harness.Catalog(cat)))

	h, err := host.Open(ctx, cat, host.Store(store))
	if err != nil {
		return
	}
	defer h.Close()
	s, err := h.Create(ctx, host.Config{
		Model:  "claude-code/claude-opus-5-5",
		Dir:    repoDir,
		System: "Review the diff. Report findings. Do not push.",
		Deny:   []string{"Bash(git push:*)"},
	})
	if err != nil {
		return
	}
	_ = h.Send(ctx, s.ID, host.Text("Review the diff against main."))
}

// A program changes the model and the mode of a session. The next run
// starts the agent again with them.
func ExampleHost_Update() {
	ctx := context.Background()
	var h *host.Host // a host from Open
	var s host.Session

	s, err := h.Update(ctx, s.ID, func(c *host.Config) {
		c.Model = "claude-code/claude-sonnet-5-5"
		c.Mode = agent.ModePlan
	})
	if err != nil {
		return
	}
	fmt.Println(s.Model, s.Mode)
}

// A reader of the host shows the status of each session, and confirms
// each tool call that waits. The mode of the config decides the calls
// that wait.
func ExampleHost_Confirm() {
	ctx := context.Background()
	var h *host.Host // a host from Open

	_, events := h.Events(ctx)
	for e, err := range events {
		if err != nil {
			return
		}
		fmt.Println(e.Session.ID, e.Session.Status, e.Session.StopReason)
		for _, req := range e.Session.Waiting {
			allow := req.Call.Name == "Read"
			_ = h.Confirm(ctx, e.Session.ID, req.Call.ID, agent.Decision{Allow: allow})
		}
	}
}

// A reader of a session applies each agent event to the state of the
// agent, and shows the partial message and the messages.
func ExampleHost_Stream() {
	ctx := context.Background()
	var h *host.Host // a host from Open

	state, events, err := h.Stream(ctx, "s_1")
	if err != nil {
		return
	}
	for ev, err := range events {
		if err != nil {
			return
		}
		state.Apply(ev)
		fmt.Println(state.Streaming(), len(state.Messages()))
	}
}
