package agent_test

import (
	"context"
	"errors"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
)

// A BeforeTool hook blocks a tool for every call of the agent. The
// model reads the reason as an error result.
func ExampleBeforeTool() {
	var lm ai.LanguageModel // a model from a catalog

	readOnly := agent.BeforeTool(func(ctx context.Context, s agent.State, c ai.ToolCall) (ai.ToolCall, error) {
		if c.Name == "bash" {
			return c, agent.Deny("bash is off in read-only mode")
		}
		return c, nil
	})
	a := agent.New(lm, readOnly)
	_ = a
}

// A BeforeCall hook gives the first call of a run a higher thinking
// level, so the model plans the work before it calls a tool.
func ExampleBeforeCall() {
	var lm ai.LanguageModel // a model from a catalog

	planFirst := agent.BeforeCall(func(ctx context.Context, s agent.State, r agent.Request) (agent.Request, error) {
		if s.Turn() == 1 {
			r.Options = ai.JoinOptions(r.Options, ai.Thinking(ai.ThinkingHigh))
		}
		return r, nil
	})
	a := agent.New(lm, planFirst)
	_ = a
}

// A BeforeCall hook caps the turns of a run. The run fails before the
// model call of turn 21.
func ExampleBeforeCall_maxTurns() {
	var lm ai.LanguageModel // a model from a catalog

	maxTurns := agent.BeforeCall(func(ctx context.Context, s agent.State, r agent.Request) (agent.Request, error) {
		if s.Turn() > 20 {
			return r, errors.New("the run is over 20 turns")
		}
		return r, nil
	})
	a := agent.New(lm, maxTurns)
	_ = a
}

// A BeforeStop hook continues a run that stopped before the work was
// done.
func ExampleBeforeStop() {
	var lm ai.LanguageModel // a model from a catalog
	testsPass := func() bool { return true }

	untilGreen := agent.BeforeStop(func(ctx context.Context, s agent.State) ([]session.Entry, error) {
		if testsPass() {
			return nil, nil
		}
		return []session.Entry{session.Text("The tests fail. Fix them.")}, nil
	})
	a := agent.New(lm, untilGreen)
	_ = a
}

// Budget is a policy at two points of the loop. It embeds
// [agent.BaseHook] and implements two points, so the loop skips it at
// the other points. It keeps no count, because the state has the usage
// of the run.
type Budget struct {
	agent.BaseHook
	// Low is the number of output tokens after which the model thinks
	// less.
	Low int
	// Max is the number of output tokens after which no tool runs.
	Max int
}

// BeforeCall lowers the thinking level after Low output tokens.
func (b Budget) BeforeCall(ctx context.Context, s agent.State, r agent.Request) (agent.Request, error) {
	if s.Usage().Output > b.Low {
		r.Options = ai.JoinOptions(r.Options, ai.Thinking(ai.ThinkingLow))
	}
	return r, nil
}

// BeforeTool blocks every tool call after Max output tokens.
func (b Budget) BeforeTool(ctx context.Context, s agent.State, c ai.ToolCall) (ai.ToolCall, error) {
	if s.Usage().Output > b.Max {
		return c, agent.Deny("the token budget is spent")
	}
	return c, nil
}

// A struct hook implements the points that it needs.
func ExampleHooks() {
	var lm ai.LanguageModel // a model from a catalog

	a := agent.New(lm, agent.Hooks(Budget{Low: 40_000, Max: 50_000}))
	_ = a
}
