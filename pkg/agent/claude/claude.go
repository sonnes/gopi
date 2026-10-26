package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"

	"github.com/sonnes/gopi/internal/transcript"
	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/internal/emit"
	"github.com/sonnes/gopi/pkg/agent/internal/resume"
	"github.com/sonnes/gopi/pkg/agent/internal/sections"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
	"github.com/sonnes/gopi/pkg/stream"
)

// defaultBinary is the name of the CLI, looked up on PATH.
const defaultBinary = "claude"

// conversation is the session ID of the CLI, so that a [Durable] agent
// resumes the same CLI session.
var conversation = resume.New("claude")

// ErrNoConversation is the error of a run whose CLI cannot find the
// stored session, such as after the session file was deleted. The agent
// clears the stored session ID, so the next run starts a new session of
// the CLI. The log keeps its messages, and the CLI no longer reads them.
var ErrNoConversation = errors.New("claude: the CLI has no conversation to resume")

// ErrAuth is the error of a run whose CLI is not logged in. Run `claude
// /login`, or set an API key in the environment of the CLI.
var ErrAuth = errors.New("claude: the CLI is not logged in")

// Permission is a permission mode of the CLI. It decides which tool
// calls the CLI runs without a prompt. The agent denies every prompt,
// so a mode that prompts less lets the CLI do more.
type Permission string

const (
	// PermissionDefault prompts for each tool call that the settings do
	// not allow.
	PermissionDefault Permission = "default"
	// PermissionAcceptEdits allows file edits without a prompt.
	PermissionAcceptEdits Permission = "acceptEdits"
	// PermissionPlan lets the model read and plan, and run no tool that
	// changes a file.
	PermissionPlan Permission = "plan"
	// PermissionAuto lets a classifier of the CLI allow or deny each
	// prompt.
	PermissionAuto Permission = "auto"
	// PermissionDontAsk denies each tool call that the settings do not
	// allow, without a prompt.
	PermissionDontAsk Permission = "dontAsk"
	// PermissionBypass allows every tool call without a prompt.
	PermissionBypass Permission = "bypassPermissions"
)

// Agent runs the Claude Code CLI as a gopi agent. The CLI runs its own
// loop and its own tools. The agent sends the input of each run to the
// CLI, turns the output into [agent.Event] values, and writes every
// message to its [session.Log].
type Agent struct {
	log *session.Log
	// store keeps the log and the child sessions of the subagents. It
	// is nil for an agent of [New], whose children are unsaved too.
	store session.Storer
	name  string
	cfg   config
	state *emit.State

	mu sync.Mutex
	// proc is the running CLI, or nil before the first run and after a
	// failure that stopped it.
	proc *process

	// steerMu guards steer. steer holds the entries of the steers that
	// no run sent. wake tells the active run that a steer waits.
	steerMu sync.Mutex
	steer   []session.Entry
	wake    chan struct{}
}

// config collects the options before [New] or [Durable] builds the agent.
type config struct {
	store      session.Storer
	name       string
	binary     string
	dir        string
	model      string
	sections   agent.SystemFunc
	thinking   ai.ThinkingLevel
	env        []string
	permission Permission
	allowed    []string
	disallowed []string
	ask        []string
	approve    agent.ApproveFunc
	hooks      []agent.Hook
	mcp        []agent.MCPServer
}

// Option configures an [Agent]. [New] and [Durable] take the same options.
type Option func(*config)

// Store sets the store of a [Durable] agent. Without it, Durable keeps
// the session in a [session.MemoryStore]. [New] ignores it.
func Store(s session.Storer) Option {
	return func(c *config) { c.store = s }
}

// Name sets the Author of every entry that the agent writes.
func Name(s string) Option {
	return func(c *config) { c.name = s }
}

// Binary sets the path of the CLI. Without it, the agent runs "claude"
// from PATH.
func Binary(path string) Option {
	return func(c *config) { c.binary = path }
}

// Dir sets the working directory of the CLI. Without it, the CLI runs
// in the working directory of this process.
func Dir(path string) Option {
	return func(c *config) { c.dir = path }
}

// Model sets the model of the CLI, as an alias such as "sonnet" or a
// full model name.
func Model(s string) Option {
	return func(c *config) { c.model = s }
}

// System appends s to the system prompt of the CLI, as one section
// named [agent.SystemSection]. It is [Sections] with
// [agent.SystemText].
func System(s string) Option {
	return Sections(agent.SystemText(s))
}

// Sections sets the function that returns the sections that the agent
// appends to the system prompt of the CLI. The CLI keeps its own
// prompt, because its tools depend on it. The agent calls fn before
// each run. The first sections of a session go in the system prompt,
// and the log keeps them as its base, so each start of the CLI gets
// the same prompt. A later change goes as a reminder before the input
// of the next run, so the cache of the conversation stays. If fn
// returns an error, the run fails. Without Sections, the agent adds no
// prompt.
func Sections(fn agent.SystemFunc) Option {
	return func(c *config) { c.sections = fn }
}

// Thinking sets the effort level of the CLI. [ai.ThinkingOff] turns
// thinking off. Without it, the CLI uses its default.
func Thinking(level ai.ThinkingLevel) Option {
	return func(c *config) { c.thinking = level }
}

// Env adds KEY=VALUE pairs to the environment of the CLI. The CLI gets
// the environment of this process, then these pairs.
func Env(kv ...string) Option {
	return func(c *config) { c.env = append(c.env, kv...) }
}

// AllowedTools lets the CLI run the named tools without a prompt. A
// name can hold a rule, such as "Bash(git:*)". Without it, the settings
// of the CLI decide.
func AllowedTools(names ...string) Option {
	return func(c *config) { c.allowed = append(c.allowed, names...) }
}

// DisallowedTools removes the named tools from the CLI. A name can hold
// a rule, as in [AllowedTools].
func DisallowedTools(names ...string) Option {
	return func(c *config) { c.disallowed = append(c.disallowed, names...) }
}

// AskTools makes the CLI ask before it runs the named tools, also when
// [AllowedTools] allows them. A name can hold a rule, as in
// AllowedTools. The agent gives the rules to the CLI as settings, and
// the CLI adds them to the ask rules of its own settings.
func AskTools(names ...string) Option {
	return func(c *config) { c.ask = append(c.ask, names...) }
}

// Approve sets the function that answers each permission prompt of the
// CLI. The call of the request has the tool_use_id, the tool name, and
// the input of the prompt. The request has the reason of the CLI, and
// has NoAlways if the CLI does not offer "allow always". Without
// Approve, the agent denies every prompt. If fn returns
// an error, the agent sends the error to the CLI, which denies the call.
//
// An Always decision sends the permission suggestions of the CLI back,
// so that the CLI does not ask again for the calls that match them. The
// CLI decides what matches. The agent sets the destination of each
// suggestion to the session, so the CLI writes no rule to a settings
// file. A suggestion can change the mode of the CLI for the session,
// such as to acceptEdits after an edit. If the CLI offers no
// suggestions, or if the prompt has NoAlways, Always does nothing. The
// suggestions describe the original input, so an Always decision with
// a new Input allows only this call. An Input that holds the same JSON
// value as the input of the prompt is not a new Input.
func Approve(fn agent.ApproveFunc) Option {
	return func(c *config) { c.approve = fn }
}

// MCP gives the CLI the MCP servers servers, with --mcp-config. The CLI
// connects to them and names each tool "mcp__<server>__<tool>". The
// servers of the settings of the CLI stay, so a server of servers
// replaces a server of the settings with the same name.
func MCP(servers ...agent.MCPServer) Option {
	return func(c *config) { c.mcp = append(c.mcp, servers...) }
}

// PermissionMode sets the permission mode of the CLI. Without it, the
// settings of the CLI decide.
func PermissionMode(mode Permission) Option {
	return func(c *config) { c.permission = mode }
}

// New returns an agent over an unsaved session. The history lives in
// process memory and ends with the process. New does no I/O. The CLI
// starts on the first run.
func New(opts ...Option) *Agent {
	a := newAgent(opts)
	a.log = session.New()
	a.state = emit.NewState(a.log)
	return a
}

// Durable returns an agent over the session id. If id is unknown,
// Durable creates it. If id exists, Durable resumes it, and the CLI
// resumes its own session. The [Store] option sets where the session
// lives.
func Durable(ctx context.Context, id string, opts ...Option) (*Agent, error) {
	a := newAgent(opts)
	store := a.cfg.store
	if store == nil {
		store = session.NewMemoryStore()
	}
	log, err := session.Open(ctx, store, id)
	if err != nil {
		return nil, err
	}
	a.log = log
	a.store = store
	a.state = emit.NewState(log)
	return a, nil
}

// newConfig applies opts over the defaults.
func newConfig(opts []Option) config {
	c := config{binary: defaultBinary}
	for _, opt := range opts {
		opt(&c)
	}
	return c
}

// newAgent applies opts and returns an agent with no log.
func newAgent(opts []Option) *Agent {
	c := newConfig(opts)
	return &Agent{
		name: c.name,
		cfg:  c,
		wake: make(chan struct{}, 1),
	}
}

// Log returns the log that the agent writes to.
func (a *Agent) Log() *session.Log { return a.log }

// State returns a copy of the state of the agent, as
// [agent.Agent.State] does. Each event of a run changes the state
// before the stream gets it. [agent.State.Log] is the log of the
// agent, also in the state that a hook of [Hooks] gets. The agent has
// no queues, so [agent.State.Steer] and [agent.State.FollowUp] do
// nothing.
func (a *Agent) State() agent.State { return a.state.Get() }

// Run sends entries to the CLI as one user message and streams the run
// until the CLI reports its result. The text of the entries is the
// input. Images and PDFs go to the CLI as image and document blocks. A
// file of another type becomes a text note, so that the model knows that
// the file was there. A run needs an entry with text or a file, or it
// fails with no event.
//
// An ephemeral entry goes to the CLI, and the log does not keep it.
// The CLI keeps it in its own transcript, so a later run of the CLI
// still reads it. A custom entry goes to the log, and not to the CLI.
//
// The first run starts the CLI, and later runs reuse it. Runs on one
// Agent are sequential. If a run is active, the returned stream fails
// with no event.
//
// If ctx ends, the agent interrupts the CLI and waits for its result.
// The stream then fails with the error of ctx.
func (a *Agent) Run(ctx context.Context, entries ...session.Entry) *agent.Stream {
	return stream.New(func(push func(agent.Event)) ([]ai.Message, error) {
		if len(emit.Input(entries)) == 0 {
			return nil, errors.New("claude: a run needs an entry with text or a file")
		}
		em := emit.New(a.log, a.name, a.state, push)
		if err := em.Start(); err != nil {
			return nil, fmt.Errorf("claude: %w", err)
		}
		base, change, err := sections.ForCLI(ctx, a.cfg.sections, a.log.Messages())
		if err != nil {
			return em.Finish(ctx, ai.Usage{}, fmt.Errorf("claude: %w", err))
		}
		// The base goes in the log before the CLI starts, because the
		// CLI gets it as its system prompt.
		if base != nil {
			if err := em.Input(ctx, []session.Entry{session.NewEntry(*base)}); err != nil {
				return em.Finish(ctx, ai.Usage{}, err)
			}
		}
		// Start the CLI before the input, so that a failed start stores
		// no input that the CLI never got.
		proc, err := a.ensure(ctx)
		if err != nil {
			return em.Finish(ctx, ai.Usage{}, err)
		}
		// The error of a hook belongs to the run that called it.
		proc.hookErr = nil
		r := newRunner(a, proc, em)
		if change != nil {
			r.reminder = transcript.Reminder(transcript.SystemText(change.Content))
			entries = append([]session.Entry{session.NewEntry(*change)}, entries...)
		}
		msgs, err := r.run(ctx, entries)
		// The CLI did not take these steers before the run ended. It
		// must not run them after the run, so the agent stops it, and
		// the host sends them again.
		if lost := r.lostSteers(); len(lost) > 0 {
			a.giveBack(lost)
			a.drop(proc)
		}
		return msgs, err
	})
}

// Close stops the CLI. It ends the input of the CLI and waits for it to
// exit. After a timeout, it kills the CLI. Close must not overlap a
// run. A later run starts the CLI again.
func (a *Agent) Close() error {
	a.mu.Lock()
	proc := a.proc
	a.proc = nil
	a.mu.Unlock()
	if proc == nil {
		return nil
	}
	return proc.stop()
}

// ensure returns the running CLI. It starts the CLI and initializes it
// if none runs.
func (a *Agent) ensure(ctx context.Context) (*process, error) {
	a.mu.Lock()
	proc := a.proc
	a.mu.Unlock()
	if proc != nil {
		return proc, nil
	}

	cmd, err := a.command()
	if err != nil {
		return nil, err
	}
	if err := agent.CheckHooks(a.cfg.hooks, hookPoints...); err != nil {
		return nil, fmt.Errorf("claude: %w", err)
	}
	proc, err = launch(cmd, a.cfg.approve, a.cfg.hooks, a.State)
	if err != nil {
		return nil, err
	}
	if _, err := proc.initialize(ctx); err != nil {
		proc.terminate()
		if errors.Is(err, ErrNoConversation) {
			err = errors.Join(err, conversation.Clear(ctx, a.log))
		}
		return nil, err
	}

	a.mu.Lock()
	a.proc = proc
	a.mu.Unlock()
	return proc, nil
}

// drop stops proc after a failure, so that the next run starts a new
// CLI that resumes the session.
func (a *Agent) drop(proc *process) {
	proc.terminate()
	a.mu.Lock()
	if a.proc == proc {
		a.proc = nil
	}
	a.mu.Unlock()
}

// askSettings returns the settings of the CLI, as JSON, that hold the
// ask rules.
func askSettings(rules []string) (string, error) {
	var s struct {
		Permissions struct {
			Ask []string `json:"ask"`
		} `json:"permissions"`
	}
	s.Permissions.Ask = rules
	data, err := json.Marshal(s)
	if err != nil {
		return "", fmt.Errorf("claude: encode the ask rules: %w", err)
	}
	return string(data), nil
}

// mcpEntry is one server of an mcp.json file, as the CLI reads it.
type mcpEntry struct {
	Type    string            `json:"type,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// mcpConfig returns servers as the JSON of an mcp.json file, for
// --mcp-config.
func mcpConfig(servers []agent.MCPServer) (string, error) {
	var f struct {
		Servers map[string]mcpEntry `json:"mcpServers"`
	}
	f.Servers = make(map[string]mcpEntry, len(servers))
	for _, s := range servers {
		f.Servers[s.Name] = mcpEntry{
			Type:    s.Type,
			Command: s.Command,
			Args:    s.Args,
			Env:     s.Env,
			URL:     s.URL,
			Headers: s.Headers,
		}
	}
	data, err := json.Marshal(f)
	if err != nil {
		return "", fmt.Errorf("claude: encode the MCP servers: %w", err)
	}
	return string(data), nil
}

// command returns the command that starts the CLI, and resumes the
// session of the CLI if the log has one.
func (a *Agent) command() (*exec.Cmd, error) {
	args, err := cliArgs(a.cfg, sections.Text(a.log.Messages()))
	if err != nil {
		return nil, err
	}
	id, err := conversation.Get(a.log)
	if err != nil {
		return nil, err
	}
	if id != "" {
		args = append(args, "--resume="+id)
	}
	return cliCommand(a.cfg, args), nil
}

// cliCommand returns the command that starts the CLI of c with args.
func cliCommand(c config, args []string) *exec.Cmd {
	cmd := exec.Command(c.binary, args...)
	cmd.Dir = c.dir
	cmd.Env = environ(c.env)
	return cmd
}

// cliArgs returns the arguments of the CLI of c, with system as the
// appended system prompt, without a session to resume. The input never goes on
// the command line. It goes to stdin.
func cliArgs(c config, system string) ([]string, error) {
	args := []string{
		"--print",
		"--output-format", "stream-json",
		"--verbose",
		"--input-format", "stream-json",
		"--include-partial-messages",
		"--replay-user-messages",
		"--permission-prompt-tool", "stdio",
	}
	if c.model != "" {
		args = append(args, "--model", c.model)
	}
	if system != "" {
		args = append(args, "--append-system-prompt", system)
	}
	switch c.thinking {
	case "":
	case ai.ThinkingOff:
		args = append(args, "--thinking", "disabled")
	default:
		args = append(args, "--effort", string(c.thinking))
	}
	if c.permission != "" {
		args = append(args, "--permission-mode", string(c.permission))
	}
	if len(c.allowed) > 0 {
		args = append(args, "--allowedTools", strings.Join(c.allowed, ","))
	}
	if len(c.disallowed) > 0 {
		args = append(args, "--disallowedTools", strings.Join(c.disallowed, ","))
	}
	if len(c.ask) > 0 {
		settings, err := askSettings(c.ask)
		if err != nil {
			return nil, err
		}
		args = append(args, "--settings", settings)
	}
	if len(c.mcp) > 0 {
		servers, err := mcpConfig(c.mcp)
		if err != nil {
			return nil, err
		}
		args = append(args, "--mcp-config", servers)
	}
	return args, nil
}
