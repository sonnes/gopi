package host_test

import (
	"context"
	"fmt"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/claude"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/catalog"
	"github.com/sonnes/gopi/pkg/harness"
	"github.com/sonnes/gopi/pkg/host"
	"github.com/sonnes/gopi/pkg/session"
)

// A host starts a session of Claude Code. The same spec with the model
// "anthropic/claude-opus-5-5" runs the model in the harness.
func ExampleHost_Start() {
	ctx := context.Background()
	store := session.NewMemoryStore() // a store that outlives the process
	repoDir := "."

	cat := catalog.New()
	cat.RegisterDriver("claude-code", claude.Driver(), claude.Models()...)
	cat.DefaultDriver(harness.New(harness.Catalog(cat)))

	h := host.New(cat, host.Store(store))
	defer h.Close()
	s, err := h.Start(ctx, host.Spec{
		Model:  "claude-code/claude-opus-5-5",
		Dir:    repoDir,
		System: "Review the diff. Report findings. Do not push.",
		Deny:   []string{"Bash(git push:*)"},
	})
	if err != nil {
		return
	}
	_ = s
}

// A message queues behind the messages before it. Send returns when the
// session accepts the message, and run_end on the host stream ends its
// run.
func ExampleSession_Send() {
	ctx := context.Background()
	var s *host.Session // a session from Host.Start

	err := s.Send(ctx, host.Text("Review the diff against main."), host.Queue)
	_ = err
}

// A reader of the host applies each event to the state, and shows the
// status of each session.
func ExampleHost_Events() {
	ctx := context.Background()
	var h *host.Host // a host from New

	state, events := h.Events(ctx)
	for ev, err := range events {
		if err != nil {
			return
		}
		state.Apply(ev)
		fmt.Println(ev.SessionID(), state.Sessions[ev.SessionID()].Status())
	}
}

// A reader of a session applies each agent event to the state of the
// agent, and shows the partial message and the messages.
func ExampleSession_Events() {
	ctx := context.Background()
	var s *host.Session // a session from Host.Start

	state, events := s.Events(ctx)
	for ev, err := range events {
		if err != nil {
			return
		}
		state.Apply(ev)
		fmt.Println(state.Streaming(), len(state.Messages()))
	}
}

// The approve function of a spec asks a person about each tool call
// that needs one. The mode of the spec decides the calls that need one.
// The function returns when its context ends, because the run ends that
// context.
func ExampleSpec_approve() {
	// ask shows the call to a person, and waits for the answer.
	ask := func(_ context.Context, c ai.ToolCall) (agent.Decision, error) {
		return agent.Decision{Allow: c.Name == "Read"}, nil
	}

	spec := host.Spec{
		Model: "claude-code/claude-opus-5-5",
		Mode:  agent.ModeAcceptEdits,
		Approve: func(ctx context.Context, c ai.ToolCall) (agent.Decision, error) {
			return ask(ctx, c)
		},
	}
	_ = spec
}
