package codex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/sonnes/gopi/internal/transcript"
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
)

// conversation is the ID of the Codex thread of the session, so that a
// [Durable] agent resumes the same thread.
var conversation = resume.New("codex")

// defaultBinary is the command that starts the CLI.
const defaultBinary = "codex"

// cliName starts each error of the agent.
const cliName = "codex"

// sandboxMode says what the commands of the CLI can touch.
type sandboxMode string

const (
	// sandboxReadOnly lets commands read files and nothing more.
	sandboxReadOnly sandboxMode = "read-only"
	// sandboxWorkspaceWrite lets commands write in the working
	// directory and in the folders of AddDirs.
	sandboxWorkspaceWrite sandboxMode = "workspace-write"
	// sandboxDangerFullAccess turns the sandbox off.
	sandboxDangerFullAccess sandboxMode = "danger-full-access"
)

// approvalPolicy says when the CLI asks for approval before it acts.
type approvalPolicy string

const (
	// approvalUntrusted asks before a command that is not known to be
	// safe.
	approvalUntrusted approvalPolicy = "untrusted"
	// approvalOnRequest lets the model decide when to ask.
	approvalOnRequest approvalPolicy = "on-request"
	// approvalNever never asks.
	approvalNever approvalPolicy = "never"
)

// Agent runs the Codex CLI. It starts the process on the first Run and
// keeps it for the next runs.
type Agent struct {
	core *cliagent.Core
	cfg  config
	// err is the error of the config. Each run of an agent of [New]
	// fails with it.
	err error

	mu sync.Mutex
	// conn is the open process, or nil.
	conn *conn
	// model is the model of the thread, from the CLI.
	model string
}

// config is the session config of an agent, its options, and what the
// agent derives from them.
type config struct {
	agent.Config
	binary string
	env    []string
	// hookCommand is the gopi binary that relays a hook.
	hookCommand string
	// policy and sandbox are the permissions of the thread. An empty
	// sandbox keeps the configured mode of the CLI.
	policy  approvalPolicy
	sandbox sandboxMode
	// hooks are the Hooks of the config, then the hooks that block the
	// tools that the config leaves out.
	hooks []agent.Hook
}

// Option sets how an agent starts the CLI. [New], [Durable], and
// [Driver] take the same options. The [agent.Config] holds the
// settings of one session.
type Option func(*config)

// Binary sets the path of the codex command. The default is "codex",
// from PATH.
func Binary(path string) Option {
	return func(c *config) { c.binary = path }
}

// Env adds environment variables to the process, as KEY=VALUE strings.
// The process gets the environment of the caller, then these.
func Env(vars ...string) Option {
	return func(c *config) { c.env = append(c.env, vars...) }
}

// New returns an agent of the config c over an unsaved log. New ignores
// the Store of c. New does no I/O, so it does not check the names of
// Skills. The process starts on the first Run. If c is not valid, each
// run fails with the error that [Durable] returns.
func New(c agent.Config, opts ...Option) *Agent {
	core := cliagent.New(cliName, &c)
	cfg, err := newConfig(c, opts)
	return &Agent{
		core: core,
		cfg:  cfg,
		err:  err,
	}
}

// Durable returns an agent of the config c over the session
// c.SessionID. Without a Store in c, Durable keeps the session in a
// [session.MemoryStore]. If the session has a thread, the agent resumes
// it. Otherwise the first Run starts a thread.
//
// Durable fails before it opens the session if c is not valid: a hook
// at a point that the CLI does not call, a mode that
// [permission.ParseMode] does not know, the limit BlockReadsOutside, a
// Deny rule that does not parse, a name of ToolNames that names a tool
// of the caller, or a name of Skills that no skill has.
func Durable(ctx context.Context, c agent.Config, opts ...Option) (*Agent, error) {
	cfg, err := newConfig(c, opts)
	if err != nil {
		return nil, err
	}
	core, err := cliagent.Open(ctx, cliName, c)
	if err != nil {
		return nil, err
	}
	return &Agent{
		core: core,
		cfg:  cfg,
	}, nil
}

// newOptions applies opts over the defaults.
func newOptions(opts []Option) config {
	c := config{
		binary:      defaultBinary,
		hookCommand: defaultHookCommand,
	}
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
	rules, err := ruleHooks(c)
	if err != nil {
		return config{}, fmt.Errorf("codex: %w", err)
	}
	if _, err := permission.ParseMode(string(c.Mode)); err != nil {
		return config{}, fmt.Errorf("codex: %w", err)
	}
	if c.Limits.BlockReadsOutside {
		return config{}, fmt.Errorf(
			"codex: the limit BlockReadsOutside: Codex has no control for reads outside the roots: %w",
			errors.ErrUnsupported,
		)
	}
	hooks := append(slices.Clip(c.Hooks), rules...)
	if err := checkHooks(hooks); err != nil {
		return config{}, err
	}
	cfg.hooks = hooks
	cfg.policy, cfg.sandbox = permissions(c)
	return cfg, nil
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

// Run appends entries to the log and runs one Codex turn with their
// text. It returns the stream of the run. If the context ends, the
// agent interrupts the turn and the stream fails with the context
// error.
//
// An ephemeral entry goes to the CLI, and the log does not keep it.
// The CLI keeps it in its thread, so a later turn still reads it. A
// custom entry goes to the log, and not to the CLI.
//
// Runs on one Agent are sequential. If a run is active, the returned
// stream fails with no event.
func (a *Agent) Run(ctx context.Context, entries ...session.Entry) *agent.Stream {
	return stream.New(func(push func(agent.Event)) ([]ai.Message, error) {
		if len(cliagent.Input(entries)) == 0 {
			return nil, errors.New("codex: a run needs an entry with text or a file")
		}
		return a.run(ctx, entries, push)
	})
}

// Close stops the process. The next Run starts a new process. The
// context of a hook in flight ends, and Close waits for the hook to
// return.
func (a *Agent) Close() error {
	a.mu.Lock()
	c := a.conn
	a.conn = nil
	a.mu.Unlock()
	var closeErr error
	if c != nil {
		closeErr = c.close()
	}
	return a.core.Close(closeErr)
}

// run is the producer behind [Agent.Run].
func (a *Agent) run(
	ctx context.Context,
	entries []session.Entry,
	push func(agent.Event),
) ([]ai.Message, error) {
	e, change, err := a.core.StartInput(ctx, push, a.cfg.Sections)
	if err != nil {
		return nil, err
	}
	// Connect before the input, so that a failed start stores no input
	// that the CLI never got.
	c, threadID, err := a.connect(ctx)
	if err != nil {
		return e.Finish(ctx, ai.Usage{}, err)
	}
	stored := entries
	if change != nil {
		if err := c.inject(ctx, threadID, transcript.SystemText(change.Content)); err != nil {
			return e.Finish(ctx, ai.Usage{}, fmt.Errorf("codex: send the change of the sections: %w", err))
		}
		stored = append([]session.Entry{session.NewEntry(*change)}, entries...)
	}
	if err := e.Input(ctx, stored); err != nil {
		return e.Finish(ctx, ai.Usage{}, err)
	}
	t := a.newTurn(c, e, threadID)
	c.bindHooks(ctx)
	usage, err := t.run(ctx, userInputs(cliagent.Input(entries), c.skills))
	// Codex does not start a turn when a child ends after the turn of
	// the run completed, so the run gives the model the result itself.
	// A report turn can start more children, so the run repeats until
	// no child ends late.
	for err == nil {
		report := t.subs.report()
		if report == "" {
			break
		}
		entry := session.Text(report)
		if err = e.Input(ctx, []session.Entry{entry}); err != nil {
			break
		}
		usage, err = t.run(ctx, userInputs(cliagent.Input([]session.Entry{entry}), def.Set{}))
	}
	// A run that ends with a child that runs, such as a turn that failed,
	// stops the child on the server. The server can still send its
	// notifications, so the process does not serve the next run.
	if t.subs.running() {
		_ = t.subs.interrupt()
		t.dead = true
	}
	return e.FinishByModel(ctx, map[string]ai.Usage{t.model: usage}, a.endTurns(ctx, c, t, err))
}

// newTurn returns the turn of a run on the thread.
func (a *Agent) newTurn(c *conn, e *cliagent.Emitter, threadID string) *turn {
	t := &turn{
		conn:     c,
		emitter:  e,
		threadID: threadID,
		model:    a.model,
		effort:   effort(a.cfg.Effort),
	}
	t.sessionID = a.core.SessionID()
	t.subs = newSubagents(a, t)
	return t
}

// endTurns ends the children and the hooks of a run, and returns err
// with the error of a hook and the errors of the logs of the children.
func (a *Agent) endTurns(ctx context.Context, c *conn, t *turn, err error) error {
	endErr := t.subs.endAll(context.WithoutCancel(ctx))
	c.bindHooks(nil)
	if err == nil {
		err = a.core.Stops().Take()
	}
	err = errors.Join(err, endErr)
	if t.dead {
		a.drop(c)
	}
	return err
}

// connect returns the open process and the thread ID. If no process
// runs, connect starts one and starts or resumes the thread.
func (a *Agent) connect(ctx context.Context) (*conn, string, error) {
	a.mu.Lock()
	c := a.conn
	a.mu.Unlock()
	threadID, err := conversation.Get(a.core.Log())
	if err != nil {
		return nil, "", err
	}
	if c != nil && c.alive() && threadID != "" {
		return c, threadID, nil
	}
	if c != nil {
		a.drop(c)
	}

	if a.err != nil {
		return nil, "", a.err
	}
	skills, skillRoot, roles, err := a.writeDefs(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("codex: %w", err)
	}
	var (
		hooks       *hookServer
		hookCommand string
	)
	if len(a.cfg.hooks) > 0 {
		hookCommand, err = a.findHookCommand()
		if err != nil {
			return nil, "", err
		}
		hooks, err = startHookServer(a.cfg.hooks, a.State, a.cfg.Approve, a.core.Stops())
		if err != nil {
			return nil, "", err
		}
	}

	cmd := a.command()
	p, err := start(cmd)
	if err != nil {
		if hooks != nil {
			hooks.close()
		}
		return nil, "", fmt.Errorf("codex: start %s: %w", cmd.Path, err)
	}
	c = dial(p, a.cfg.Approve, a.cfg.Dir)
	c.hooks = hooks
	c.skills = skills
	res, err := c.handshake(ctx, a.threadParams(threadID, hooks, hookCommand, roles), skillRoot)
	if err != nil {
		c.kill()
		if threadID != "" && lostThread(err) {
			err = errors.Join(
				fmt.Errorf("codex: %w", agent.ErrNoConversation),
				conversation.Clear(ctx, a.core.Log()),
			)
		}
		return nil, "", err
	}
	if err := conversation.Save(ctx, a.core.Log(), res.Thread.ID); err != nil {
		c.kill()
		return nil, "", err
	}

	a.mu.Lock()
	a.conn = c
	a.model = res.Model
	a.mu.Unlock()
	return c, res.Thread.ID, nil
}

// skillMention is the start of a mention of a skill that Codex loads.
const skillMention = "$"

// userInputs returns the inputs of a turn. An image goes as a data URL.
// A file of another type becomes a text note. If the first text calls a
// skill of skills, as [def.Set.Slash] reads it, its "/" and the name
// become "$" and the name that Codex knows. Codex loads a skill from a
// mention, and it reads "/" as text.
func userInputs(input []ai.Content, skills def.Set) []userInput {
	out := make([]userInput, 0, len(input))
	first := true
	for _, c := range input {
		switch c := c.(type) {
		case ai.Text:
			if first {
				c = mentionSkill(c, skills)
				first = false
			}
			out = append(out, textInput(c))
		case ai.File:
			out = append(out, fileInput(c))
		}
	}
	return out
}

// mentionSkill returns t with its call of a skill of skills as a
// mention. Otherwise it returns t.
func mentionSkill(t ai.Text, skills def.Set) ai.Text {
	name, rest, ok := skills.Slash(t.Text)
	if !ok {
		return t
	}
	t.Text = skillMention + name + rest
	return t
}

// textInput returns the text input of t.
func textInput(t ai.Text) userInput {
	return userInput{
		Type: "text",
		Text: t.Text,
	}
}

// fileInput returns the image input of f, or a text note if f is not an
// image.
func fileInput(f ai.File) userInput {
	if !strings.HasPrefix(f.MediaType, "image/") {
		return textInput(cliagent.Unread(f))
	}
	data := base64.StdEncoding.EncodeToString(f.Data)
	return userInput{
		Type: "image",
		URL:  "data:" + f.MediaType + ";base64," + data,
	}
}

// lostThreadText is in the error of thread/resume when the CLI cannot
// find the thread.
const lostThreadText = "no rollout found for thread id"

// lostThread reports whether err says that the CLI cannot find the
// thread to resume.
func lostThread(err error) bool {
	return strings.Contains(err.Error(), lostThreadText)
}

// drop stops c and forgets it, so that the next Run starts a new
// process.
func (a *Agent) drop(c *conn) {
	a.mu.Lock()
	if a.conn == c {
		a.conn = nil
	}
	a.mu.Unlock()
	c.kill()
}

// command returns the command that starts the app server.
func (a *Agent) command() *exec.Cmd {
	return appServer(a.cfg)
}

// appServer returns the command that starts the app server of c, with
// the permission keys of [permissionArgs].
func appServer(c config) *exec.Cmd {
	args := append([]string{"app-server"}, permissionArgs(c)...)
	cmd := exec.Command(c.binary, args...)
	cmd.Dir = c.Dir
	cmd.Env = cliproc.Environ(nil, c.env)
	return cmd
}

// threadParams returns the parameters of thread/start, or of
// thread/resume if thread is set. With a hook server, the config adds
// the hooks of the agent to the thread, and each hook runs the program
// hookCommand. roles is the agents table of the config, from
// [Agent.writeDefs].
func (a *Agent) threadParams(threadID string, hooks *hookServer, hookCommand string, roles map[string]any) threadParams {
	p := threadParams{
		ThreadID:              threadID,
		Model:                 a.cfg.Model.ID,
		Cwd:                   a.cfg.Dir,
		ApprovalPolicy:        a.cfg.policy,
		Sandbox:               a.cfg.sandbox,
		DeveloperInstructions: sections.Text(a.core.Log().Messages()),
	}
	if hooks != nil {
		p.Config = hookConfig(hookCommand, hooks.socket, a.cfg.hooks)
	}
	if a.cfg.AutoCompact > 0 {
		if p.Config == nil {
			p.Config = make(map[string]any)
		}
		p.Config["model_auto_compact_token_limit"] = a.cfg.AutoCompact
	}
	if len(a.cfg.MCP) > 0 {
		if p.Config == nil {
			p.Config = make(map[string]any)
		}
		p.Config["mcp_servers"] = mcpServers(a.cfg.MCP)
	}
	if len(roles) > 0 {
		if p.Config == nil {
			p.Config = make(map[string]any)
		}
		p.Config["agents"] = roles
	}
	return p
}

// permissionArgs returns the -c flags that set the permission keys of
// the config of the CLI, so the config.toml of the user does not decide
// them:
//
//   - approvals_reviewer is "user", so each approval request reaches
//     the Approve function and no automatic reviewer.
//   - sandbox_mode is the sandbox of the Mode of c, if c has one. The mode
//     selects the sandbox model of the modes, so the CLI does not read a
//     permission profile of default_permissions.
//   - The sandbox_workspace_write table has the AddDirs of c as the
//     writable_roots, no network, and the temp folders as writable.
//
// The flags do not set approval_policy. The approvalPolicy of
// thread/start sets it, and the CLI fails to load a config with the
// policy "untrusted". The config of thread/start has no
// sandbox_workspace_write table, because a table there replaces the
// table of the flags.
func permissionArgs(c config) []string {
	// The value of a -c flag is TOML. A JSON string and a JSON array of
	// strings are also TOML.
	dirs := c.AddDirs
	if dirs == nil {
		dirs = []string{}
	}
	roots, _ := json.Marshal(dirs)
	keys := []string{`approvals_reviewer="user"`}
	if c.sandbox != "" {
		mode, _ := json.Marshal(string(c.sandbox))
		keys = append(keys, "sandbox_mode="+string(mode))
	}
	keys = append(keys,
		"sandbox_workspace_write.writable_roots="+string(roots),
		"sandbox_workspace_write.network_access=false",
		"sandbox_workspace_write.exclude_tmpdir_env_var=false",
		"sandbox_workspace_write.exclude_slash_tmp=false",
	)
	args := make([]string, 0, 2*len(keys))
	for _, k := range keys {
		args = append(args, "-c", k)
	}
	return args
}

// mcpServers returns servers as the mcp_servers table of the config of
// the CLI, by name. A stdio server has command, args, and env. An HTTP
// server has url and http_headers.
func mcpServers(servers []agent.MCPServer) map[string]any {
	out := make(map[string]any, len(servers))
	for _, s := range servers {
		entry := make(map[string]any)
		if s.Type == agent.MCPHTTP {
			entry["url"] = s.URL
			if len(s.Headers) > 0 {
				entry["http_headers"] = s.Headers
			}
		} else {
			entry["command"] = s.Command
			if len(s.Args) > 0 {
				entry["args"] = s.Args
			}
			if len(s.Env) > 0 {
				entry["env"] = s.Env
			}
		}
		out[s.Name] = entry
	}
	return out
}

// findHookCommand returns the absolute path of the hook command. The
// reasons are in the doc comment of [HookCommand].
func (a *Agent) findHookCommand() (string, error) {
	path, err := exec.LookPath(a.cfg.hookCommand)
	if err != nil {
		return "", fmt.Errorf("codex: find the hook command: %w", err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("codex: find the hook command: %w", err)
	}
	return abs, nil
}

// effort returns the reasoning effort of the CLI for level, or "" for
// the default of the model. [ai.ThinkingOff] gives "", because not
// every model accepts an effort of none.
func effort(level ai.ThinkingLevel) string {
	if level == ai.ThinkingOff {
		return ""
	}
	for name, l := range efforts {
		if l == level {
			return name
		}
	}
	return ""
}
