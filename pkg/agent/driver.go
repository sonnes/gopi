package agent

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
	"github.com/sonnes/gopi/pkg/tool"
)

// Driver starts an agent of one kind for one session. A host starts
// each session through a driver, so the same code runs the harness, the
// Claude Code agent, and the Codex agent. A catalog holds the driver of
// each provider.
type Driver interface {
	// Start starts the agent of the session c.Session, and resumes the
	// session if it exists. If the driver cannot honor a field of c, or
	// a point of a hook, Start returns an error that wraps
	// [errors.ErrUnsupported] and names the field or the point.
	Start(ctx context.Context, c Config) (Runner, error)
}

// Config is what a host gives a [Driver] for one session. A zero field
// keeps the default of the driver.
type Config struct {
	// Session is the ID of the session. It is required.
	Session string
	// Store keeps the session. If it is nil, the driver keeps the
	// session in memory.
	Store session.Storer
	// Spec is the model spec of the agent, such as
	// "claude-code/claude-opus-5-5". Model is the model that the
	// catalog resolved from it. A driver that resolves models reads
	// Spec. A driver of a CLI reads the ID of Model.
	Spec  string
	Model ai.Model
	// Dir is the working directory of the agent.
	Dir string
	// System is the text that the agent adds to its own prompt.
	System string
	// Skills names the skills that the agent loads at the start.
	Skills []string
	// Mode is the permission mode of the agent. If it is empty, the
	// driver keeps its own mode.
	Mode Mode
	// Allow, Ask, and Deny are the permission rules of the agent, in
	// the format "Tool" or "Tool(content)".
	Allow []string
	Ask   []string
	Deny  []string
	// Approve decides each tool call that needs a person.
	Approve ApproveFunc
	// Hooks change the loop of the agent.
	Hooks []Hook
	// Tools are more tools for the agent.
	Tools []tool.Tool
}

// Runner is the part of an agent that a host uses. [Agent], the Claude
// Code agent, and the Codex agent implement it. A runner shows an extra
// feature with another interface, such as [Steerer] or [io.Closer].
type Runner interface {
	Run(ctx context.Context, entries ...session.Entry) *Stream
	Log() *session.Log
	State() State
}

// Steerer is a [Runner] that reads entries during a run, before its
// next model call. [Agent] implements it, and the CLI agents do not.
// A run can end before it reads a steer, so a caller takes the entries
// that no run read with TakeSteer after the run.
type Steerer interface {
	Steer(entries ...session.Entry)
	TakeSteer() []session.Entry
}

// Point names a point of the loop at which a hook runs.
type Point string

// The points of the loop, in the order of the loop.
const (
	PointBeforeCall Point = "BeforeCall"
	PointAfterCall  Point = "AfterCall"
	PointBeforeTool Point = "BeforeTool"
	PointAfterTool  Point = "AfterTool"
	PointAfterTurn  Point = "AfterTurn"
	PointBeforeStop Point = "BeforeStop"
)

// AllPoints lists each [Point], in the order of the loop.
var AllPoints = []Point{
	PointBeforeCall,
	PointAfterCall,
	PointBeforeTool,
	PointAfterTool,
	PointAfterTurn,
	PointBeforeStop,
}

// Points returns the points that h implements, in the order of the
// loop.
func Points(h Hook) []Point {
	var out []Point
	if _, ok := h.(BeforeCallHook); ok {
		out = append(out, PointBeforeCall)
	}
	if _, ok := h.(AfterCallHook); ok {
		out = append(out, PointAfterCall)
	}
	if _, ok := h.(BeforeToolHook); ok {
		out = append(out, PointBeforeTool)
	}
	if _, ok := h.(AfterToolHook); ok {
		out = append(out, PointAfterTool)
	}
	if _, ok := h.(AfterTurnHook); ok {
		out = append(out, PointAfterTurn)
	}
	if _, ok := h.(BeforeStopHook); ok {
		out = append(out, PointBeforeStop)
	}
	return out
}

// CheckHooks returns an error for the first hook that a driver cannot
// run: a hook that uses a point that is not in supported, or a hook that
// implements no point. The error for a point wraps
// [errors.ErrUnsupported]. A driver fails on such a hook, because a
// hook that guards a point must not miss a call. A hook with no point
// is a hook with a misspelled method.
func CheckHooks(hooks []Hook, supported ...Point) error {
	for i, h := range hooks {
		points := Points(h)
		if len(points) == 0 {
			return fmt.Errorf("agent: hook %d (%T) implements no point", i+1, h)
		}
		for _, p := range points {
			if !slices.Contains(supported, p) {
				return fmt.Errorf(
					"agent: hook %d (%T) uses %s: %w",
					i+1,
					h,
					p,
					errors.ErrUnsupported,
				)
			}
		}
	}
	return nil
}

// Mode is a permission mode. It decides the tool calls that no rule
// matches: a call runs, asks the approve function, or is blocked. The
// names are the names of the permission modes of Claude Code, because
// the harness uses the same names.
type Mode string

// The permission modes, from the strictest.
const (
	// ModePlan runs the read tools and blocks each other call.
	ModePlan Mode = "plan"
	// ModeDefault runs the read tools. Each other call asks.
	ModeDefault Mode = "default"
	// ModeAcceptEdits runs the read tools and the edit tools. Each
	// other call asks.
	ModeAcceptEdits Mode = "acceptEdits"
	// ModeBypass runs each call.
	ModeBypass Mode = "bypassPermissions"
)

// AllModes lists each [Mode], from the strictest.
var AllModes = []Mode{
	ModePlan,
	ModeDefault,
	ModeAcceptEdits,
	ModeBypass,
}

// CheckMode returns an error if a driver cannot honor the mode m. An
// empty m keeps the mode of the driver, so it gives no error. A mode
// that is not in supported gives an error that wraps
// [errors.ErrUnsupported]. A driver fails on such a mode, because an
// agent with another mode runs calls that its caller did not allow. A
// mode that is not in [AllModes] gives another error.
func CheckMode(m Mode, supported ...Mode) error {
	if m == "" || slices.Contains(supported, m) {
		return nil
	}
	if !slices.Contains(AllModes, m) {
		return fmt.Errorf("agent: unknown mode %q", m)
	}
	return fmt.Errorf("agent: the mode %s: %w", m, errors.ErrUnsupported)
}
