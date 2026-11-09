// Package cliagent holds the part of a CLI agent that does not depend
// on its CLI. A [Core] opens the session of the agent, starts each run,
// and removes the folder of the definitions at the close. An [Emitter]
// turns the output of the CLI into [agent.Event] values and log
// entries.
//
// The CLI runs its own loop and its own tools. An Emitter mirrors that
// loop, with the same event order as [agent.Agent]:
//
//	agent_start
//	  turn_start
//	    message_start, message_end...                    (input, turn 1 only)
//	    message_start, message_update..., message_end    (assistant)
//	    tool_execution_start, tool_execution_end,
//	    message_start, message_end                       (per tool call)
//	  turn_end
//	  ...
//	agent_end
//
// A turn is one assistant message and the tool results that follow it.
// The next assistant message starts the next turn.
//
// The log stores each message before its message_end, and the
// message_end has the ID of the entry. So a consumer that reads a
// message_end knows that the store has the message. A message_end with
// no content is the end of a message that the log does not keep.
//
// Each event changes a [State] before the stream gets it, so the state
// of a CLI agent follows its runs as the state of [agent.Agent] does.
package cliagent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/internal/clidefs"
	"github.com/sonnes/gopi/pkg/agent/internal/clihook"
	"github.com/sonnes/gopi/pkg/agent/internal/sections"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
)

// Core is the session of a CLI agent: its log, its store, its state, the
// folder of its definitions, and the error of a hook that stopped the
// CLI. It lives as long as the agent.
type Core struct {
	// name is the name of the CLI, such as "claude". Each error of the
	// core starts with it.
	name  string
	log   *session.Log
	store session.Storer
	state *State
	defs  *clidefs.Folder
	stops clihook.StopErr
}

// New returns the core of an agent of c over an unsaved log. It ignores
// the Store of c. If c has no SessionID, New gives it a new one, because
// the folder of the definitions has the name of the session.
func New(name string, c *agent.Config) *Core {
	if c.SessionID == "" {
		c.SessionID = uuid.NewString()
	}
	return newCore(name, *c, session.New(), nil)
}

// Open returns the core of an agent of c over the session c.SessionID.
// It fails if a name of Skills has no skill. Without a Store in c, Open
// keeps the session in a [session.MemoryStore].
func Open(ctx context.Context, name string, c agent.Config) (*Core, error) {
	if err := clidefs.CheckSkills(ctx, c.Defs, c.Skills); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	store := c.Store
	if store == nil {
		store = session.NewMemoryStore()
	}
	log, err := session.Open(ctx, store, c.SessionID)
	if err != nil {
		return nil, err
	}
	return newCore(name, c, log, store), nil
}

func newCore(name string, c agent.Config, log *session.Log, store session.Storer) *Core {
	return &Core{
		name:  name,
		log:   log,
		store: store,
		state: NewState(log),
		defs:  clidefs.New(c.SessionID, c.Spec),
		stops: clihook.StopErr{Agent: name},
	}
}

// Log returns the log of the agent.
func (c *Core) Log() *session.Log { return c.log }

// Store returns the store of the session, or nil for an unsaved agent.
// A child session of a subagent goes in the same store.
func (c *Core) Store() session.Storer { return c.store }

// SessionID returns the ID of the session, or "" for an unsaved agent.
func (c *Core) SessionID() string {
	if c.store == nil {
		return ""
	}
	return c.log.Session().ID
}

// State returns a copy of the state of the agent. See [State.Get].
func (c *Core) State() agent.State { return c.state.Get() }

// Defs returns the folder of the definition files of the session.
func (c *Core) Defs() *clidefs.Folder { return c.defs }

// Stops returns the error of the hook that stopped the CLI in the
// active run. Each process and hook server of the agent keeps its error
// there.
func (c *Core) Stops() *clihook.StopErr { return &c.stops }

// Start starts a run that sends its events to push, and returns its
// emitter. It fails with no event if a run is active. It clears the
// error of a hook, because the error belongs to the run that called
// the hook.
func (c *Core) Start(push func(agent.Event)) (*Emitter, error) {
	em := NewEmitter(c.log, c.state, push)
	if err := em.Start(); err != nil {
		return nil, fmt.Errorf("%s: %w", c.name, err)
	}
	_ = c.stops.Take()
	return em, nil
}

// StartInput is [Core.Start] for a run with input. Then it stores the
// base of the sections of fn, because the CLI gets the base as its
// system prompt when it starts. It returns the change of the sections,
// which goes to the CLI with the input, or nil. If a step after
// agent_start fails, StartInput ends the run and returns the error.
func (c *Core) StartInput(
	ctx context.Context,
	push func(agent.Event),
	fn agent.SystemFunc,
) (*Emitter, *ai.Message, error) {
	em, err := c.Start(push)
	if err != nil {
		return nil, nil, err
	}
	base, change, err := sections.ForCLI(
		ctx,
		fn,
		c.log.Messages(),
		c.log.History().CompactedAt,
	)
	if err != nil {
		_, err = em.Finish(ctx, ai.Usage{}, fmt.Errorf("%s: %w", c.name, err))
		return nil, nil, err
	}
	if base != nil {
		err := em.Input(ctx, []session.Entry{session.NewEntry(*base)})
		if err != nil {
			_, err = em.Finish(ctx, ai.Usage{}, err)
			return nil, nil, err
		}
	}
	return em, change, nil
}

// Close removes the folder of the definitions, and returns stopErr, the
// error of the stop of the CLI, joined with the error of the removal.
func (c *Core) Close(stopErr error) error {
	return errors.Join(stopErr, c.defs.Remove())
}

// InterruptTimeout is how long a cancelled run waits for the CLI to end
// its turn after the interrupt. Then the agent kills the CLI, and the
// next run starts a new one.
const InterruptTimeout = 5 * time.Second

// Interrupt watches the cancel of a run in the select of its loop. A
// case on Done sees the cancel. Then the run calls [Interrupt.Fire],
// sends the interrupt to the CLI, and goes on to read the CLI, so that
// the log keeps what the CLI ends. A case on Deadline ends the wait.
type Interrupt struct {
	// Done is the Done channel of the context of the run, and nil after
	// Fire.
	Done <-chan struct{}
	// Deadline is nil before Fire. It sends [InterruptTimeout] after
	// Fire.
	Deadline <-chan time.Time
}

// Watch returns the Interrupt of a run with the context ctx.
func Watch(ctx context.Context) *Interrupt {
	return &Interrupt{Done: ctx.Done()}
}

// Fire starts the wait for the end of the turn.
func (i *Interrupt) Fire() {
	i.Done = nil
	i.Deadline = time.After(InterruptTimeout)
}
