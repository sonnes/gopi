package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/internal/cliagent"
	"github.com/sonnes/gopi/pkg/agent/internal/cliproc"
	"github.com/sonnes/gopi/pkg/agent/internal/resume"
	"github.com/sonnes/gopi/pkg/agent/internal/sections"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/def"
	"github.com/sonnes/gopi/pkg/permission"
	"github.com/sonnes/gopi/pkg/session"
	"github.com/sonnes/gopi/pkg/stream"
	"github.com/sonnes/gopi/pkg/tool"
)

// defaultBinary is the name of the CLI, looked up on PATH.
const defaultBinary = "claude"

// cliName starts each error of the agent.
const cliName = "claude"

// conversation is the session ID of the CLI, so that a [Durable] agent
// resumes the same CLI session.
var conversation = resume.New("claude")

// Agent runs the Claude Code CLI as a gopi agent. The CLI runs its own
// loop and its own tools. The agent sends the input of each run to the
// CLI, turns the output into [agent.Event] values, and writes every
// message to its [session.Log].
type Agent struct {
	core *cliagent.Core
	cfg  config
	// err is the error of the config. Each run of an agent of [New]
	// fails with it.
	err error

	// mu guards proc and idle. proc is the running CLI, or nil before
	// the first run and after a failure that stopped it. idle is the
	// reader of the CLI between runs, or nil during a run.
	mu   sync.Mutex
	proc *process
	idle *idleReader

	// steerMu guards steer. steer holds the entries of the steers that
	// no run sent. steerReady tells the active run that a steer waits.
	steerMu    sync.Mutex
	steer      []session.Entry
	steerReady chan struct{}

	// subs tracks the subagents, and kept are the lines of a turn that
	// the CLI started between runs. A run and the idle reader take turns
	// with them: [Agent.pauseIdle] waits for the reader to stop.
	subs *subagents
	kept []*line
}

// config is the session config of an agent, its options, and what the
// agent derives from them.
type config struct {
	agent.Config
	binary string
	env    []string
	// tools are the gopi names of the tools that --tools gives the CLI.
	// If tools is nil, the CLI keeps its default tools, as for a list
	// of the models.
	tools []string
	// hooks are the Hooks of the config, then the hook of ToolNames.
	hooks []agent.Hook
}

// Option sets how an agent starts the CLI. [New], [Durable], and
// [Driver] take the same options. The [agent.Config] holds the
// settings of one session.
type Option func(*config)

// Binary sets the path of the CLI. Without it, the agent runs "claude"
// from PATH.
func Binary(path string) Option {
	return func(c *config) { c.binary = path }
}

// Env adds KEY=VALUE pairs to the environment of the CLI. The CLI gets
// the environment of this process, then these pairs.
func Env(kv ...string) Option {
	return func(c *config) { c.env = append(c.env, kv...) }
}

// New returns an agent of the config c over an unsaved session. The
// history lives in process memory and ends with the process. New
// ignores the Store of c. New does no I/O, so it does not check the
// names of Skills. The CLI starts on the first run. If c is not valid,
// each run fails with the error that [Durable] returns.
func New(c agent.Config, opts ...Option) *Agent {
	core := cliagent.New(cliName, &c)
	cfg, err := newConfig(c, opts)
	return newAgent(core, cfg, err)
}

// Durable returns an agent of the config c over the session
// c.SessionID. If the session is unknown, Durable creates it. If it
// exists, Durable resumes it, and the CLI resumes its own session.
// Without a Store in c, Durable keeps the session in a
// [session.MemoryStore].
//
// Durable fails before it opens the session if c is not valid: a hook
// at a point that the CLI does not call, a mode that
// [permission.ParseMode] does not know, a name of ToolNames that names
// a tool of the caller, or a name of Skills that no skill has.
func Durable(ctx context.Context, c agent.Config, opts ...Option) (*Agent, error) {
	cfg, err := newConfig(c, opts)
	if err != nil {
		return nil, err
	}
	core, err := cliagent.Open(ctx, cliName, c)
	if err != nil {
		return nil, err
	}
	return newAgent(core, cfg, nil), nil
}

// newOptions applies opts over the defaults.
func newOptions(opts []Option) config {
	c := config{binary: defaultBinary}
	for _, opt := range opts {
		opt(&c)
	}
	return c
}

// newConfig returns the config of an agent of c with opts. It does no
// I/O.
func newConfig(c agent.Config, opts []Option) (config, error) {
	cfg := newOptions(opts)
	cfg.Config = c
	tools, err := cliTools(c.Tools, c.ToolNames)
	if err != nil {
		return config{}, fmt.Errorf("claude: %w", err)
	}
	if _, err := permission.ParseMode(string(c.Mode)); err != nil {
		return config{}, fmt.Errorf("claude: %w", err)
	}
	hooks := slices.Clip(c.Hooks)
	if c.ToolNames != nil {
		// --tools hides only the built-in tools, so the hook blocks a
		// call of an MCP tool that ToolNames does not name.
		hooks = append(hooks, agent.OnlyTools(c.ToolNames))
	}
	if err := checkHooks(hooks); err != nil {
		return config{}, err
	}
	cfg.tools = tools
	cfg.hooks = hooks
	return cfg, nil
}

// newAgent returns an agent of core with cfg. Each run fails with err,
// the error of the config.
func newAgent(core *cliagent.Core, cfg config, err error) *Agent {
	a := &Agent{
		core:       core,
		cfg:        cfg,
		err:        err,
		steerReady: make(chan struct{}, 1),
	}
	a.subs = newSubagents(a)
	return a
}

// Log returns the log that the agent writes to.
func (a *Agent) Log() *session.Log { return a.core.Log() }

// State returns a copy of the state of the agent, as
// [agent.Agent.State] does. Each event of a run changes the state
// before the stream gets it. [agent.State.Log] is the log of the
// agent, also in the state that a hook of the config gets. The agent has
// no queues, so [agent.State.Steer] and [agent.State.FollowUp] do
// nothing.
func (a *Agent) State() agent.State { return a.core.State() }

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
		if len(cliagent.Input(entries)) == 0 {
			return nil, errors.New("claude: a run needs an entry with text or a file")
		}
		return a.runWith(ctx, push, entries)
	})
}

// Close stops the CLI. It ends the input of the CLI and waits for it to
// exit. After a timeout, it kills the CLI. Close must not overlap a
// run. A later run starts the CLI again.
func (a *Agent) Close() error {
	a.pauseIdle()
	a.mu.Lock()
	proc := a.proc
	a.proc = nil
	a.mu.Unlock()
	// No child in the background outlives its CLI.
	a.endBackground()
	var stopErr error
	if proc != nil {
		stopErr = proc.stop()
	}
	return a.core.Close(stopErr)
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

	if a.err != nil {
		return nil, a.err
	}
	cmd, fresh, skills, err := a.command(ctx)
	if err != nil {
		return nil, err
	}
	proc, err = launch(cmd, a.cfg.Approve, a.cfg.hooks, a.State, a.core.Stops())
	if err != nil {
		return nil, err
	}
	proc.skills = skills
	if proc.models, err = a.usageBase(); err != nil {
		proc.terminate()
		return nil, err
	}
	if _, err := proc.initialize(ctx); err != nil {
		proc.terminate()
		switch {
		case errors.Is(err, agent.ErrNoConversation):
			err = errors.Join(err, conversation.Clear(ctx, a.core.Log()))
		case fresh != "" && takenSession(err, fresh):
			// An earlier CLI made the conversation and exited before the
			// agent stored its ID. The agent stores the ID now, so the
			// next start resumes the conversation.
			if err := conversation.Save(ctx, a.core.Log(), fresh); err != nil {
				return nil, err
			}
			return a.ensure(ctx)
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

// cliSettings are the settings of the CLI that the agent gives with
// --settings.
type cliSettings struct {
	Permissions struct {
		BlockReadsOutside  bool `json:"blockReadsOutsideWorkingDirectories,omitempty"`
		BlockWritesOutside bool `json:"blockWritesOutsideWorkingDirectories,omitempty"`
	} `json:"permissions"`
	Sandbox *sandboxSettings `json:"sandbox,omitempty"`
}

// sandboxSettings are the sandbox settings of the CLI.
type sandboxSettings struct {
	Enabled bool `json:"enabled"`
}

// settings returns the settings of the CLI, as JSON, that hold the
// limits. ok is false if there is nothing to give.
func settings(l permission.Limits) (string, bool, error) {
	if l == (permission.Limits{}) {
		return "", false, nil
	}
	var s cliSettings
	s.Permissions.BlockReadsOutside = l.BlockReadsOutside
	s.Permissions.BlockWritesOutside = l.BlockWritesOutside
	if l.Fence {
		s.Sandbox = &sandboxSettings{Enabled: true}
	}
	data, err := json.Marshal(s)
	if err != nil {
		return "", false, fmt.Errorf("claude: encode the settings: %w", err)
	}
	return string(data), true, nil
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

// command returns the command that starts the CLI, and the skills that
// the CLI gets. If the log has the session of a CLI, the command resumes
// it. Otherwise a saved agent gives the CLI the session ID of
// [cliSessionID], and fresh is that ID.
func (a *Agent) command(ctx context.Context) (cmd *exec.Cmd, fresh string, skills def.Set, err error) {
	args, err := cliArgs(a.cfg, sections.Text(a.core.Log().Messages()))
	if err != nil {
		return nil, "", def.Set{}, err
	}
	skills, folder, err := writeDefs(ctx, a.cfg.Defs, a.core.Defs())
	if err != nil {
		return nil, "", def.Set{}, fmt.Errorf("claude: %w", err)
	}
	if folder != "" {
		args = append(args, "--add-dir", folder)
	}
	for _, dir := range a.cfg.AddDirs {
		args = append(args, "--add-dir", dir)
	}
	id, err := conversation.Get(a.core.Log())
	if err != nil {
		return nil, "", def.Set{}, err
	}
	switch {
	case id != "":
		args = append(args, "--resume="+id)
	case a.core.SessionID() != "":
		fresh = cliSessionID(a.core.SessionID())
		args = append(args, "--session-id="+fresh)
	}
	return cliCommand(a.cfg, args), fresh, skills, nil
}

// usageBase returns the modelUsage that a CLI that resumes the stored
// conversation starts from.
func (a *Agent) usageBase() (map[string]wireModelUsage, error) {
	id, err := conversation.Get(a.core.Log())
	if err != nil {
		return nil, err
	}
	return loadBase(a.core.Log(), id)
}

// cliSpace is the UUID namespace of the session IDs that the agent
// gives the CLI.
var cliSpace = uuid.NewSHA1(uuid.NameSpaceURL, []byte("gopi://claude"))

// cliSessionID returns the session ID of the CLI for the gopi session
// id: a UUIDv5, because the CLI takes only a UUID. One gopi session
// always gives the same session of the CLI.
func cliSessionID(id string) string {
	return uuid.NewSHA1(cliSpace, []byte(id)).String()
}

// takenSessionText is in the error of a CLI whose new session ID has a
// conversation already.
const takenSessionText = "Session ID %s is already in use"

// takenSession reports whether err says that the session ID fresh of the
// CLI has a conversation already.
func takenSession(err error, fresh string) bool {
	return strings.Contains(err.Error(), fmt.Sprintf(takenSessionText, fresh))
}

// cliCommand returns the command that starts the CLI of c with args.
func cliCommand(c config, args []string) *exec.Cmd {
	cmd := exec.Command(c.binary, args...)
	cmd.Dir = c.Dir
	cmd.Env = cliproc.Environ(dropped, c.env)
	return cmd
}

// cliModes maps each permission mode to the name of the mode in the
// CLI.
var cliModes = map[permission.Mode]string{
	permission.Default:     "default",
	permission.AcceptEdits: "acceptEdits",
	permission.Bypass:      "bypassPermissions",
}

// cliArgs returns the arguments of the CLI of c, with system as the
// appended system prompt, without a session to resume. The input never
// goes on the command line. It goes to stdin.
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
	if c.Model.ID != "" {
		args = append(args, "--model", c.Model.ID)
	}
	if c.AutoCompact > 0 {
		args = append(args, "--autocompact", strconv.Itoa(c.AutoCompact))
	}
	if system != "" {
		args = append(args, "--append-system-prompt", system)
	}
	switch c.Effort {
	case "":
	case ai.ThinkingOff:
		args = append(args, "--thinking", "disabled")
	default:
		args = append(args, "--effort", string(c.Effort))
	}
	if c.Mode != "" {
		args = append(args, "--permission-mode", cliModes[c.Mode])
	}
	if removed := tool.WholeTools(c.Deny); len(removed) > 0 {
		rules := claudeRules(removed)
		args = append(args, "--disallowedTools", strings.Join(rules, ","))
	}
	s, ok, err := settings(c.Limits)
	if err != nil {
		return nil, err
	}
	if ok {
		args = append(args, "--settings", s)
	}
	if c.tools != nil {
		args = append(args, "--tools", strings.Join(builtinTools(c.tools), ","))
	}
	if len(c.MCP) > 0 {
		servers, err := mcpConfig(c.MCP)
		if err != nil {
			return nil, err
		}
		args = append(args, "--mcp-config", servers)
	}
	return args, nil
}
