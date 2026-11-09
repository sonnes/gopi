package harness

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/catalog"
	"github.com/sonnes/gopi/pkg/def"
	"github.com/sonnes/gopi/pkg/mcp"
	"github.com/sonnes/gopi/pkg/permission"
	"github.com/sonnes/gopi/pkg/sandbox"
	"github.com/sonnes/gopi/pkg/sandbox/fence"
	"github.com/sonnes/gopi/pkg/session"
	"github.com/sonnes/gopi/pkg/tool"
)

// SessionModel is the session value that keeps the model spec of a
// session from [Harness.Start]. A caller reads it to show the model of
// a session. The harness writes it; a caller does not.
var SessionModel = session.Value[string]{
	Namespace: "gopi.harness",
	Key:       "model",
}

// Harness holds the builders of an agent: the catalog, the source of
// the definitions, the readers of tools, the Allow rules, the
// prompt builder, the seeders, and the agent options. It builds nothing
// until a call of [Harness.New] or [Harness.Start]. One harness can
// serve many sessions, and the [agent.Config] of a call holds the
// settings of one session, such as the Dir of one repository.
type Harness struct {
	cat   *catalog.Catalog
	defs  def.Source
	tools []ToolsReader
	box   sandbox.Sandbox
	// fenced is true if the default sandbox runs in the fence of
	// [Fence], and fence holds the options of Fence.
	fenced    bool
	fence     []fence.Option
	system    string
	prompt    PromptBuilder
	seeds     []Seeder
	agentOpts []agent.Option
	allow     []string

	// cfg is the config of the session of a call.
	cfg agent.Config
	// store keeps the session of a call of [Harness.Start].
	store session.Storer
	// mcpTools are the tools of the MCP servers of cfg, after they
	// connect.
	mcpTools []tool.Tool
	// child is the subagent of a call that the agent tool makes. It is
	// nil in the call of a caller.
	child *childCall
	// mem keeps the sessions of [Harness.Start] when the config has no
	// Store, so a second call with the same id resumes the session.
	mem session.Storer
}

// New returns a harness with opts.
func New(opts ...Option) *Harness {
	h := &Harness{mem: session.NewMemoryStore()}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// call returns a copy of h for one session with the config c. The lists
// of the copy have no spare capacity, so an append to a list of the
// copy does not write to a list of h. Calls on one harness can then run
// at the same time. call checks the hooks of c before an MCP server
// connects, so that a bad hook fails the call and opens no session.
func (h *Harness) call(c agent.Config) (*Harness, error) {
	if _, err := permission.ParseMode(string(c.Mode)); err != nil {
		return nil, fmt.Errorf("harness: %w", err)
	}
	if err := agent.CheckHooks(c.Hooks, agent.AllPoints...); err != nil {
		return nil, fmt.Errorf("harness: %w", err)
	}
	call := *h
	call.tools = slices.Clip(call.tools)
	call.seeds = slices.Clip(call.seeds)
	call.agentOpts = slices.Clip(call.agentOpts)
	call.fence = slices.Clip(call.fence)
	call.allow = slices.Clip(call.allow)
	call.mcpTools = slices.Clip(call.mcpTools)
	call.cfg = c
	return &call, nil
}

// New assembles an agent of the config c over an unsaved session. It
// reads the readers, makes the agent, and seeds its log. The agent
// builds the prompt before each model call. New ignores the SessionID
// and the Store of c. A subagent of an unsaved agent is unsaved too, so
// the agent tool cannot resume it. A hook of c that implements no point
// fails New, as in [Harness.Start].
func (h *Harness) New(ctx context.Context, c agent.Config) (*agent.Agent, error) {
	call, err := h.call(c)
	if err != nil {
		return nil, err
	}
	return call.newAgent(ctx)
}

// newAgent is [Harness.New] after the call has its config.
func (h *Harness) newAgent(ctx context.Context) (*agent.Agent, error) {
	call, closeMCP, err := h.connectMCP(ctx)
	if err != nil {
		return nil, err
	}
	a, err := call.unsaved(ctx, closeMCP)
	if err != nil {
		return nil, errors.Join(err, closeMCP())
	}
	return a, nil
}

// unsaved is [Harness.New] after the MCP servers connect. The agent
// runs closeMCP on [agent.Agent.Close].
func (h *Harness) unsaved(ctx context.Context, closeMCP func() error) (*agent.Agent, error) {
	asm, err := h.assemble(ctx, "")
	if err != nil {
		return nil, err
	}
	lm, agentOpts, err := asm.prepare()
	if err != nil {
		return nil, err
	}

	agentOpts = append(agentOpts, agent.OnClose(closeMCP))
	a := agent.New(lm, agentOpts...)
	if err := asm.seed(ctx, a.Log()); err != nil {
		return nil, err
	}
	return a, nil
}

// Start assembles the agent of the config c over the session
// c.SessionID. A harness is the default [agent.Driver] of a catalog: it
// runs a model of each text provider. If the session is unknown, Start
// creates it. If it exists, Start resumes it. Without a Store in c, the
// sessions of the harness live in one memory store and end with the
// process. Only a session with no entries gets the seed. The runner is
// an [*agent.Agent].
//
// The session keeps its model spec. The Spec of c changes the model of
// the session. Without a Spec, the session keeps its model.
//
// The harness honors each field of c, each mode, and each point of a
// hook, and it ignores Model, Idle, and Wake:
//
//   - Dir is the folder that the agent works in. The tools of
//     [Sandboxed] start in it. Without Dir, the harness uses the working
//     directory of the process. The harness reads no file of Dir: give
//     the definitions with [Defs] or the Defs of c.
//   - The permission policy reads and edits in each folder of AddDirs
//     without a question, as in Dir. With [Sandbox], the Dir of the
//     sandbox replaces Dir in this rule. The fence of [Fence] writes in
//     AddDirs. A sandbox of [Sandbox] ignores them.
//   - The harness loads the Defs of c after the source of [Defs], so a
//     definition of c wins for a name.
//   - The body of each skill of Skills goes in the base text of the
//     prompt, after the text of [System], as a subagent does with the
//     skills of its file. A name is the name of a skill in the skill
//     list, or its qualified name. If no skill has a name, Start fails.
//   - The agent calls Sections before each model call, and puts the
//     sections after the sections of the prompt builder. A subagent does
//     not get them, because the file of the subagent is its prompt. If
//     a name repeats, the run fails.
//   - Mode decides each tool call that no rule matches. Without Mode,
//     the harness uses [permission.Default]. The Deny rules of c go
//     after the [Allow] rules of h. A Deny rule with no content, such as
//     "WebFetch", also removes the tool from the agent, as Claude Code
//     does, so the model does not see it.
//   - The policy checks BlockReadsOutside and BlockWritesOutside of
//     Limits. Fence turns on [Fence] with no options.
//   - Approve asks a person to approve a tool call. Without Approve,
//     each call that needs an approval is denied, and the model gets the
//     reason. Approve also answers a hook of c that returns an
//     [agent.AskError].
//   - The Tools of c go after the tools of [Toolset]. ToolNames keeps
//     only the named tools of the agent: it applies to each tool of
//     Toolset, each tool of c, and each MCP tool. A name of no tool
//     fails Start with [tool.ErrUnknownTool].
//   - Start connects to the servers of MCP with [mcp.Connect], and the
//     agent gets their tools as deferred tools named
//     "mcp__<server>__<tool>". A stdio server gets the environment of
//     the process. If a server does not connect, Start fails.
//     [agent.Agent.Close] closes the sessions, and a subagent shares
//     the sessions of its parent.
//   - Effort, Hooks, and Compaction go after the options of
//     [AgentDefaults]. AutoCompact gives the strategy Summarize of the
//     package compact with that limit, unless c has a Compaction. A
//     hook of Hooks that implements no point fails Start before an MCP
//     server connects.
func (h *Harness) Start(ctx context.Context, c agent.Config) (agent.Runner, error) {
	call, err := h.call(c)
	if err != nil {
		return nil, err
	}
	a, err := call.start(ctx)
	if err != nil {
		return nil, err
	}
	return a, nil
}

// start is [Harness.Start] after the call has its config.
func (h *Harness) start(ctx context.Context) (*agent.Agent, error) {
	stored, err := h.resolve(ctx)
	if err != nil {
		return nil, err
	}
	call, closeMCP, err := h.connectMCP(ctx)
	if err != nil {
		return nil, err
	}
	a, err := call.durable(ctx, stored, closeMCP)
	if err != nil {
		return nil, errors.Join(err, closeMCP())
	}
	return a, nil
}

// durable is [Harness.Start] after the MCP servers connect. stored is
// the spec that the session keeps. The agent runs closeMCP on
// [agent.Agent.Close].
func (h *Harness) durable(ctx context.Context, stored string, closeMCP func() error) (*agent.Agent, error) {
	id := h.cfg.SessionID
	asm, err := h.assemble(ctx, id)
	if err != nil {
		return nil, err
	}
	lm, agentOpts, err := asm.prepare()
	if err != nil {
		return nil, err
	}

	agentOpts = append(
		agentOpts,
		agent.Store(h.store),
		agent.OnClose(closeMCP),
	)
	a, err := agent.Durable(ctx, lm, id, agentOpts...)
	if err != nil {
		return nil, err
	}
	w := a.Log().Writer()
	if h.cfg.Spec != stored {
		if err := SessionModel.Set(ctx, w, h.cfg.Spec); err != nil {
			return nil, err
		}
	}
	if h.child != nil && len(a.Log().Entries()) == 0 {
		if err := agent.Parent.Set(ctx, w, h.child.parent); err != nil {
			return nil, err
		}
	}
	if err := asm.seed(ctx, a.Log()); err != nil {
		return nil, err
	}
	return a, nil
}

// connectMCP connects to the MCP servers of the config, and returns a
// copy of h with their tools and with no servers, and the function that
// closes the sessions. The agent tool builds a subagent from the copy,
// so a subagent shares the sessions of its parent and connects to
// nothing. If a server does not connect, connectMCP closes the others
// and fails.
func (h *Harness) connectMCP(ctx context.Context) (*Harness, func() error, error) {
	noop := func() error { return nil }
	if len(h.cfg.MCP) == 0 {
		return h, noop, nil
	}
	tools, closeMCP, err := mcp.Connect(ctx, h.cfg.MCP, os.Environ())
	if err != nil {
		return nil, nil, errors.Join(fmt.Errorf("harness: %w", err), closeMCP())
	}
	call := *h
	call.mcpTools = append(slices.Clip(h.mcpTools), tools...)
	call.cfg.MCP = nil
	return &call, closeMCP, nil
}

// resolve sets the store and the spec of the session of h, and returns
// the spec that the session keeps, or "" if there is none.
//
// It reads the session once to learn its model spec. It reads the
// store directly, so that it does not create a session before the
// model is known. If the session exists, the store of h gives the
// snapshot that resolve read to the first load of the session.
func (h *Harness) resolve(ctx context.Context) (string, error) {
	id := h.cfg.SessionID
	if id == "" {
		return "", errors.New("harness: the session id is required")
	}
	// A subagent has the store of its parent already.
	if h.store == nil {
		h.store = h.cfg.Store
	}
	if h.store == nil {
		h.store = h.mem
	}

	snap, err := h.store.Load(ctx, id)
	switch {
	case errors.Is(err, session.ErrNotFound):
		return "", nil
	case err != nil:
		return "", err
	}

	stored, _, err := SessionModel.Read(snap)
	if err != nil {
		return "", fmt.Errorf("harness: session %s: %w", id, err)
	}
	// A Spec in the config changes the model of the session.
	if h.cfg.Spec == "" {
		h.cfg.Spec = stored
	}
	h.store = session.Preloaded(h.store, id, snap)
	return stored, nil
}
