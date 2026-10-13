// Package codex runs the OpenAI Codex CLI as a gopi agent. An [Agent]
// starts `codex app-server` and talks JSON-RPC to it over stdio. The
// CLI runs its own loop and its own tools. The Agent turns the output
// into [agent.Event] values with the same order as [agent.Agent], and
// it writes each message to a [session.Log].
//
// A caller must obey these rules:
//
//   - Runs on one Agent are sequential. A second Run fails on its
//     stream.
//   - A Run needs an entry with text or a file, because a Codex turn
//     needs input.
//   - Call [Agent.Close] when the agent is done. Close stops the
//     process.
//
// The Codex thread ID is a value on the log, so a [Durable] agent over
// a stored session resumes the same thread.
//
// The agent declines every approval request from the CLI, unless an
// [Approver] answers it. To let the CLI act without a request, set
// [Approval] to [ApprovalNever] and pick a [Sandbox] mode.
//
// The reasons are in docs/concepts/agent/cli-agent.md.
package codex

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/internal/emit"
	"github.com/sonnes/gopi/pkg/agent/internal/resume"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
	"github.com/sonnes/gopi/pkg/stream"
)

// conversation is the ID of the Codex thread of the session, so that a
// [Durable] agent resumes the same thread.
var conversation = resume.New("codex")

// defaultBinary is the command that starts the CLI.
const defaultBinary = "codex"

// ErrNoConversation is the error of a run whose CLI cannot find the
// stored thread, such as after its rollout was deleted. The agent clears
// the stored thread ID, so the next run starts a new thread. The log
// keeps its messages, and the CLI no longer reads them.
var ErrNoConversation = errors.New("codex: the CLI has no conversation to resume")

// ErrAuth is the error of a run whose CLI is not logged in. Run `codex
// login`, or set an API key in the environment of the CLI.
var ErrAuth = errors.New("codex: the CLI is not logged in")

// SandboxMode says what the commands of the CLI can touch.
type SandboxMode string

const (
	// SandboxReadOnly lets commands read files and nothing more.
	SandboxReadOnly SandboxMode = "read-only"
	// SandboxWorkspaceWrite lets commands write in the working
	// directory.
	SandboxWorkspaceWrite SandboxMode = "workspace-write"
	// SandboxDangerFullAccess turns the sandbox off.
	SandboxDangerFullAccess SandboxMode = "danger-full-access"
)

// ApprovalPolicy says when the CLI asks for approval before it acts.
// An [Approver] answers each request. Without it, the agent declines
// every request.
type ApprovalPolicy string

const (
	// ApprovalUntrusted asks before a command that is not known to be
	// safe.
	ApprovalUntrusted ApprovalPolicy = "untrusted"
	// ApprovalOnRequest lets the model decide when to ask.
	ApprovalOnRequest ApprovalPolicy = "on-request"
	// ApprovalNever never asks. It is the default, because the agent
	// has no user to ask.
	ApprovalNever ApprovalPolicy = "never"
)

// Agent runs the Codex CLI. It starts the process on the first Run and
// keeps it for the next runs.
type Agent struct {
	cfg   config
	log   *session.Log
	state *emit.State

	mu sync.Mutex
	// conn is the open process, or nil.
	conn *conn
	// model is the model of the thread, from the CLI.
	model string
}

// config collects the options before [New] or [Durable] builds the
// agent.
type config struct {
	store    session.Storer
	name     string
	binary   string
	dir      string
	model    string
	system   string
	thinking ai.ThinkingLevel
	env      []string
	sandbox  SandboxMode
	approval ApprovalPolicy
	approver ApproverFunc
}

// Option configures an [Agent]. [New] and [Durable] take the same
// options.
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

// Binary sets the path of the codex command. The default is "codex",
// from PATH.
func Binary(path string) Option {
	return func(c *config) { c.binary = path }
}

// Dir sets the working directory of the process and of the thread.
// Without it, the process uses the working directory of the caller.
func Dir(path string) Option {
	return func(c *config) { c.dir = path }
}

// Model sets the model of the thread. Without it, the CLI uses its
// configured model.
func Model(name string) Option {
	return func(c *config) { c.model = name }
}

// System sets the developer instructions of the thread. The CLI keeps
// its own base instructions.
func System(s string) Option {
	return func(c *config) { c.system = s }
}

// Thinking sets the reasoning effort of each turn. [ai.ThinkingMax]
// becomes "xhigh", the highest effort of the CLI. [ai.ThinkingOff]
// keeps the default effort of the model, because not every model
// accepts an effort of none.
func Thinking(level ai.ThinkingLevel) Option {
	return func(c *config) { c.thinking = level }
}

// Env adds environment variables to the process, as KEY=VALUE strings.
// The process gets the environment of the caller, then these.
func Env(vars ...string) Option {
	return func(c *config) { c.env = append(c.env, vars...) }
}

// Sandbox sets the sandbox mode of the thread. Without it, the CLI
// uses its configured mode.
func Sandbox(mode SandboxMode) Option {
	return func(c *config) { c.sandbox = mode }
}

// Approval sets the approval policy of the thread. The default is
// [ApprovalNever].
func Approval(policy ApprovalPolicy) Option {
	return func(c *config) { c.approval = policy }
}

// ApprovalKind is the kind of action that an [ApprovalRequest] asks
// for.
type ApprovalKind string

const (
	// ApprovalCommand asks to run a command.
	ApprovalCommand ApprovalKind = "command"
	// ApprovalFileChange asks to change files.
	ApprovalFileChange ApprovalKind = "fileChange"
)

// ApprovalRequest is a request of the CLI to run a command or to change
// files.
type ApprovalRequest struct {
	Kind ApprovalKind
	// ItemID is the ID of the tool call in the log.
	ItemID string
	// Command and Cwd are set for [ApprovalCommand].
	Command string
	Cwd     string
	// Reason says why the CLI asks, or is empty.
	Reason string
}

// Decision answers an [ApprovalRequest]. The zero value declines.
type Decision string

const (
	// Decline refuses the action. The turn goes on.
	Decline Decision = ""
	// Accept allows the action one time.
	Accept Decision = "accept"
	// AcceptForSession allows the action, and the actions that the CLI
	// matches to it, until the process stops.
	AcceptForSession Decision = "acceptForSession"
	// Cancel refuses the action and interrupts the turn.
	Cancel Decision = "cancel"
)

// ApproverFunc answers an approval request of the CLI. It runs while
// the CLI waits, so it must return when ctx ends. The ctx ends when the
// process stops.
type ApproverFunc func(ctx context.Context, req ApprovalRequest) (Decision, error)

// Approver sets the function that answers each approval request of the
// CLI. Without it, the agent declines every request. If fn returns an
// error, the agent declines the request.
func Approver(fn ApproverFunc) Option {
	return func(c *config) { c.approver = fn }
}

// New returns an agent over an unsaved log. New does no I/O. The
// process starts on the first Run.
func New(opts ...Option) *Agent {
	return &Agent{
		cfg:   newConfig(opts),
		log:   session.New(),
		state: emit.NewState(nil),
	}
}

// Durable returns an agent over the session id. The [Store] option
// sets where the session lives. If the session has a thread, the agent
// resumes it. Otherwise the first Run starts a thread.
func Durable(ctx context.Context, id string, opts ...Option) (*Agent, error) {
	cfg := newConfig(opts)
	store := cfg.store
	if store == nil {
		store = session.NewMemoryStore()
	}
	log, err := session.Open(ctx, store, id)
	if err != nil {
		return nil, err
	}
	return &Agent{
		cfg:   cfg,
		log:   log,
		state: emit.NewState(log.Messages()),
	}, nil
}

// newConfig applies opts over the defaults.
func newConfig(opts []Option) config {
	c := config{
		binary:   defaultBinary,
		approval: ApprovalNever,
	}
	for _, opt := range opts {
		opt(&c)
	}
	return c
}

// Log returns the log that the agent writes to.
func (a *Agent) Log() *session.Log { return a.log }

// State returns a copy of the state of the agent, as
// [agent.Agent.State] does. Each event of a run changes the state
// before the stream gets it. The agent has no queues and no hooks, so
// [agent.State.Steer] and [agent.State.FollowUp] do nothing, and
// [agent.State.Log] returns nil. Use [Agent.Log] for the log.
func (a *Agent) State() agent.State { return a.state.Get() }

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
		if len(emit.Input(entries)) == 0 {
			return nil, errors.New("codex: a run needs an entry with text or a file")
		}
		return a.run(ctx, entries, push)
	})
}

// Close stops the process. The next Run starts a new process.
func (a *Agent) Close() error {
	a.mu.Lock()
	c := a.conn
	a.conn = nil
	a.mu.Unlock()
	if c == nil {
		return nil
	}
	return c.close()
}

// run is the producer behind [Agent.Run].
func (a *Agent) run(
	ctx context.Context,
	entries []session.Entry,
	push func(agent.Event),
) ([]ai.Message, error) {
	e := emit.New(a.log, a.cfg.name, a.state, push)
	if err := e.Start(); err != nil {
		return nil, fmt.Errorf("codex: %w", err)
	}
	// Connect before the input, so that a failed start stores no input
	// that the CLI never got.
	c, thread, err := a.connect(ctx)
	if err != nil {
		return e.Finish(ctx, ai.Usage{}, err)
	}
	if err := e.Input(ctx, entries); err != nil {
		return e.Finish(ctx, ai.Usage{}, err)
	}
	t := &turn{
		conn:     c,
		emitter:  e,
		threadID: thread,
		model:    a.model,
		effort:   effort(a.cfg.thinking),
	}
	usage, err := t.run(ctx, userInputs(emit.Input(entries)))
	if t.dead {
		a.drop(c)
	}
	return e.Finish(ctx, usage, err)
}

// connect returns the open process and the thread ID. If no process
// runs, connect starts one and starts or resumes the thread.
func (a *Agent) connect(ctx context.Context) (*conn, string, error) {
	a.mu.Lock()
	c := a.conn
	a.mu.Unlock()
	thread, err := conversation.Get(a.log)
	if err != nil {
		return nil, "", err
	}
	if c != nil && c.alive() && thread != "" {
		return c, thread, nil
	}
	if c != nil {
		a.drop(c)
	}

	cmd := a.command()
	p, err := start(cmd)
	if err != nil {
		return nil, "", fmt.Errorf("codex: start %s: %w", cmd.Path, err)
	}
	c = dial(p, a.cfg.approver)
	res, err := c.handshake(ctx, a.threadParams(thread))
	if err != nil {
		c.kill()
		if thread != "" && lostThread(err) {
			err = errors.Join(ErrNoConversation, conversation.Clear(ctx, a.log))
		}
		return nil, "", err
	}
	if err := conversation.Save(ctx, a.log, res.Thread.ID); err != nil {
		c.kill()
		return nil, "", err
	}

	a.mu.Lock()
	a.conn = c
	a.model = res.Model
	a.mu.Unlock()
	return c, res.Thread.ID, nil
}

// userInputs returns the inputs of a turn. An image goes as a data URL.
// A file of another type becomes a text note.
func userInputs(input []ai.Content) []userInput {
	out := make([]userInput, 0, len(input))
	for _, c := range input {
		switch c := c.(type) {
		case ai.Text:
			out = append(out, textInput(c))
		case ai.File:
			out = append(out, fileInput(c))
		}
	}
	return out
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
		return textInput(emit.Unread(f))
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
	cmd := exec.Command(a.cfg.binary, "app-server")
	cmd.Dir = a.cfg.dir
	if len(a.cfg.env) > 0 {
		cmd.Env = append(os.Environ(), a.cfg.env...)
	}
	return cmd
}

// threadParams returns the parameters of thread/start, or of
// thread/resume if thread is set.
func (a *Agent) threadParams(thread string) threadParams {
	return threadParams{
		ThreadID:              thread,
		Model:                 a.cfg.model,
		Cwd:                   a.cfg.dir,
		ApprovalPolicy:        a.cfg.approval,
		Sandbox:               a.cfg.sandbox,
		DeveloperInstructions: a.cfg.system,
	}
}

// effort returns the reasoning effort of the CLI for level, or "" for
// the default of the model.
func effort(level ai.ThinkingLevel) string {
	switch level {
	case ai.ThinkingLow, ai.ThinkingMedium, ai.ThinkingHigh, ai.ThinkingXHigh:
		return string(level)
	case ai.ThinkingMax:
		return string(ai.ThinkingXHigh)
	default:
		return ""
	}
}
