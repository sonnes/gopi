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
	// Start starts the agent of the session c.SessionID, and resumes the
	// session if it exists. If the driver cannot honor a field of c, or
	// a point of a hook, Start returns an error that wraps
	// [errors.ErrUnsupported] and names the field or the point.
	Start(ctx context.Context, c Config) (Runner, error)
}

// Config is what a host gives a [Driver] for one session. A zero field
// keeps the default of the driver.
type Config struct {
	// SessionID is the ID of the session. It is required.
	SessionID string
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
	// Sections returns the sections that the agent adds to its own
	// prompt, in order. A native agent calls it before each model call,
	// and an agent CLI calls it before each run. A change of the
	// sections reaches the agent as a change in the conversation, so the
	// start of the conversation stays the same for the cache. If it is
	// nil, the agent adds no prompt.
	Sections SystemFunc
	// Skills names the skills that the agent loads at the start.
	Skills []string
	// Dirs are folders with agents/, skills/, and AGENTS.md, lowest
	// first. A later folder wins for a name. The harness reads them. A
	// driver of a CLI skips them, because the caller gives the CLI its
	// own skills and agents.
	Dirs []string
	// Effort is the thinking level of each model call. If it is empty,
	// the driver keeps the default of the model.
	Effort ai.ThinkingLevel
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
	// MCP are the MCP servers of the agent. The agent gets the tools
	// of each server.
	MCP []MCPServer
	// Idle gets the events that the agent sends between its runs, such
	// as the events of a subagent in the background. A driver that has
	// no such events ignores it. Without it, the agent drops them.
	Idle func(Event)
}

// Checker is a [Driver] that reports its [Status]: whether it can start
// an agent, and with which account. The Claude Code and Codex drivers
// implement it. A caller asks with a type assertion, as for [Steerer]. A
// check uses the setup of the driver, such as its binary and its
// environment, so a caller sees what a session would start.
type Checker interface {
	// Check reports the status of the driver. If the driver needs a
	// program that is not installed, the error wraps [ErrNotInstalled].
	// A driver with no account signed in gives LoggedIn false and no
	// error.
	Check(ctx context.Context) (Status, error)
}

// Status is what a [Checker] reports about its driver. It never holds a
// key or a token. It can hold the email of the account, so a caller
// decides who sees it.
type Status struct {
	// Version is the version of what the driver runs, such as
	// "2.1.308" for Claude Code. It is empty if the driver has none.
	Version string
	// LoggedIn reports that an account is signed in.
	LoggedIn bool
	// Method is how the driver signs in. It is empty if no account is
	// signed in, or if the driver does not say.
	Method AuthMethod
	// Plan names the plan or the cloud of the account for a person, such
	// as "Claude Max", "ChatGPT Plus", or "Amazon Bedrock". It is empty
	// if the driver does not say.
	Plan string
	// Account is the email of the account. It is empty if the driver
	// does not say.
	Account string
	// Org is the name of the organization of the account. It is empty if
	// the driver does not say.
	Org string
}

// AuthMethod is how a driver signs in to the provider of its models.
type AuthMethod string

// The methods of a [Status].
const (
	// AuthSubscription is a subscription for a person, such as Claude
	// Max or ChatGPT Plus.
	AuthSubscription AuthMethod = "subscription"
	// AuthAPIKey is an API key, from the environment or from a helper.
	AuthAPIKey AuthMethod = "api_key"
	// AuthCloud is the account of a cloud, such as Amazon Bedrock or
	// Google Vertex AI. The plan names the cloud.
	AuthCloud AuthMethod = "cloud"
)

// ErrNotInstalled is the error of a [Checker] whose program is not
// installed, such as a CLI that is not at its path.
var ErrNotInstalled = errors.New("agent: the CLI is not installed")

// Runner is the part of an agent that a host uses. [Agent], the Claude
// Code agent, and the Codex agent implement it. A runner shows an extra
// feature with another interface, such as [Steerer] or [io.Closer].
type Runner interface {
	Run(ctx context.Context, entries ...session.Entry) *Stream
	Log() *session.Log
	State() State
}

// Steerer is a [Runner] that reads entries during a run, before its
// next model call. [Agent] and the Claude Code agent implement it, and
// the Codex agent does not.
// A run can end before it reads a steer, so a caller takes the entries
// that no run read with TakeSteer after the run.
type Steerer interface {
	Steer(entries ...session.Entry)
	TakeSteer() []session.Entry
}

// Waker is a [Runner] whose agent can start a turn on its own, such as
// the Claude Code agent when background work ends. Wakes sends one value
// when a turn starts outside a run. Continue runs that turn: it sends no
// input, and it ends at the end of the turn. A caller that sees a wake
// while a run is active does nothing, because the run reads the turn.
type Waker interface {
	Wakes() <-chan struct{}
	Continue(ctx context.Context) *Stream
}

// KindWake is the custom entry that a run of [Waker.Continue] adds first,
// so that a viewer of the log shows where the agent went on by itself.
const KindWake = "gopi.wake"

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
