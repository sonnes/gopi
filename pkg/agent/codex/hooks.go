package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/internal/hooktext"
	"github.com/sonnes/gopi/pkg/agent/internal/jsonx"
	"github.com/sonnes/gopi/pkg/ai"
)

// The hook events of the CLI at the points of a gopi hook.
const (
	eventPreToolUse  = "PreToolUse"
	eventPostToolUse = "PostToolUse"
	eventStop        = "Stop"
)

// hookPoints are the points of a gopi hook that the CLI calls. The CLI
// has no hook at a model call and no hook after each turn.
var hookPoints = []agent.Point{
	agent.PointBeforeTool,
	agent.PointAfterTool,
	agent.PointBeforeStop,
}

// defaultHookCommand is the program that relays a hook of the CLI to
// the agent, when no [HookCommand] is set.
const defaultHookCommand = "gopi"

// The texts of a hook answer that the agent gives.
const (
	renameText      = "codex: a hook cannot rename a tool call of Codex"
	commandText     = "codex: a hook must give Bash and apply_patch an input with a string command"
	unreachableText = "the gopi host of the hook is not reachable"
)

// The names that the tool calls of a hook have in the CLI, and the
// prefix of an MCP tool.
const (
	hookBash      = "Bash"
	hookMCPPrefix = "mcp__"
)

// Hooks adds hooks that the CLI calls at its own hook events. Codex has
// no hook callback over its protocol, so the agent adds a command hook
// to each thread through the config of thread/start, with the hook
// trust bypassed. The command, [HookCommand], relays each call to a
// socket of the agent:
//
//   - BeforeTool at PreToolUse. An error denies the call with its text.
//     A new input allows the call with it. A new name denies the call.
//     For Bash and apply_patch, a new input must be an object with a
//     string command. A new input of another shape denies the call.
//   - AfterTool at PostToolUse. A new text, or the text of an error,
//     replaces the output that the model reads.
//   - BeforeStop at Stop. The text of the entries continues the turn.
//     An error stops the turn and fails the run.
//
// If the command fails or times out, Codex runs the tool. If the
// command cannot reach the agent, it denies a call at PreToolUse, gives
// the error as the output at PostToolUse, and ends the turn at Stop.
// The hooks do not see a hosted tool, such as web search. A hook gives
// only text entries. If a hook uses BeforeCall, AfterCall, or
// AfterTurn, the run fails with an error that wraps
// [errors.ErrUnsupported].
func Hooks(hs ...agent.Hook) Option {
	return func(c *config) { c.hooks = append(c.hooks, hs...) }
}

// HookCommand sets the path of the gopi binary, which relays a hook of
// the CLI to the agent with "gopi hook". The default is "gopi" on the
// PATH. A program that runs the agent in its own process is not the
// gopi binary, so the default is not [os.Executable].
//
// The agent finds the command with [exec.LookPath] when it starts the
// process. If the command is not there, the run fails, because Codex
// runs the tool when a hook command is missing. If the path is
// relative, the agent makes it absolute, because Codex runs the hook
// in the thread directory.
func HookCommand(path string) Option {
	return func(c *config) { c.hookCommand = path }
}

// hookHandler is one command hook in the config of a thread.
type hookHandler struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

// hookGroup is the list of handlers of one hook event.
type hookGroup struct {
	Hooks []hookHandler `json:"hooks"`
}

// hookConfig returns the config of thread/start that adds a command
// hook for each event that a hook uses. The command relays the hook to
// socket.
func hookConfig(command, socket string, hooks []agent.Hook) map[string]any {
	line := shellQuote(command) + " hook " + shellQuote(socket)
	events := make(map[string][]hookGroup)
	for _, h := range hooks {
		for _, p := range agent.Points(h) {
			event := eventOf(p)
			if event == "" {
				continue
			}
			events[event] = []hookGroup{{Hooks: []hookHandler{{
				Type:    "command",
				Command: line,
			}}}}
		}
	}
	if len(events) == 0 {
		return nil
	}
	return map[string]any{
		"bypass_hook_trust": true,
		"hooks":             events,
	}
}

// eventOf returns the hook event of the CLI at the point p, or "".
func eventOf(p agent.Point) string {
	switch p {
	case agent.PointBeforeTool:
		return eventPreToolUse
	case agent.PointAfterTool:
		return eventPostToolUse
	case agent.PointBeforeStop:
		return eventStop
	}
	return ""
}

// shellQuote quotes s for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// hookInput is the input of a command hook, on its stdin.
type hookInput struct {
	HookEventName string          `json:"hook_event_name"`
	ToolName      string          `json:"tool_name"`
	ToolInput     json.RawMessage `json:"tool_input"`
	ToolResponse  json.RawMessage `json:"tool_response"`
	ToolUseID     string          `json:"tool_use_id"`
}

// hookOutput is the answer of a command hook, on its stdout. The zero
// value changes nothing.
type hookOutput struct {
	Continue           *bool         `json:"continue,omitempty"`
	StopReason         string        `json:"stopReason,omitempty"`
	Decision           string        `json:"decision,omitempty"`
	Reason             string        `json:"reason,omitempty"`
	HookSpecificOutput *hookSpecific `json:"hookSpecificOutput,omitempty"`
}

type hookSpecific struct {
	HookEventName            string          `json:"hookEventName"`
	PermissionDecision       string          `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string          `json:"permissionDecisionReason,omitempty"`
	UpdatedInput             json.RawMessage `json:"updatedInput,omitempty"`
}

// hookServer answers the hook calls of one process of the CLI on a unix
// socket. Each connection carries one call: the input, then the answer.
type hookServer struct {
	ln     net.Listener
	dir    string
	socket string
	hooks  []agent.Hook
	state  func() agent.State
	// ctx ends when the server closes. A hook between runs gets it. A
	// hook in a run gets a context that ends with the run or with ctx.
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu sync.Mutex
	// err is the error of a hook that stopped the turn.
	err error
	// runCtx is the context of the run in progress, or nil.
	runCtx context.Context
}

// hookSocket is the name of the socket in the folder of a server.
const hookSocket = "hook.sock"

// hookReadTimeout is how long the server waits for the input of a hook
// call. gopi hook reads its input before it connects, so the input
// arrives at once. Without the limit, a stuck connection blocks the
// close of the server. Tests shorten it.
var hookReadTimeout = 10 * time.Second

// maxSocketPath is the longest path of a unix socket on every OS that
// gopi supports. macOS has the smallest limit: 104 bytes with the
// closing NUL.
const maxSocketPath = 103

// shortTempDir is the parent of the folder of the socket when the path
// in the temp folder of the OS is too long.
const shortTempDir = "/tmp"

// hookDir makes a new folder for the socket in the temp folder of the
// OS. If the path of the socket is too long there, hookDir makes the
// folder in [shortTempDir].
func hookDir() (string, error) {
	dir, err := os.MkdirTemp("", "gopi-hook-")
	if err != nil {
		return "", err
	}
	fits := len(filepath.Join(dir, hookSocket)) <= maxSocketPath
	if fits {
		return dir, nil
	}
	_ = os.RemoveAll(dir)
	return os.MkdirTemp(shortTempDir, "gopi-hook-")
}

// startHookServer listens on a socket in a new folder, and serves the
// hooks.
func startHookServer(hooks []agent.Hook, state func() agent.State) (*hookServer, error) {
	dir, err := hookDir()
	if err != nil {
		return nil, fmt.Errorf("codex: make the folder of the hook socket: %w", err)
	}
	socket := filepath.Join(dir, hookSocket)
	ln, err := net.Listen("unix", socket)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("codex: listen for hooks: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &hookServer{
		ln:     ln,
		dir:    dir,
		socket: socket,
		hooks:  hooks,
		state:  state,
		ctx:    ctx,
		cancel: cancel,
	}
	s.wg.Add(1)
	go s.serve()
	return s, nil
}

func (s *hookServer) serve() {
	defer s.wg.Done()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handle(conn)
		}()
	}
}

// handle reads one hook input from conn, and writes its answer. Codex
// goes on when a hook gives no answer, so a failure of the server gets
// the failure answer of its event. See [failure].
func (s *hookServer) handle(conn net.Conn) {
	defer conn.Close()
	err := conn.SetReadDeadline(time.Now().Add(hookReadTimeout))
	var data []byte
	if err == nil {
		data, err = io.ReadAll(conn)
	}
	var out []byte
	if err == nil {
		out, err = s.reply(data)
	}
	if err != nil {
		out, _ = json.Marshal(s.fail(eventName(data), err))
	}
	_, _ = conn.Write(out)
}

// fail returns the answer to a call at event that the server could not
// answer. A failed Stop also fails the run.
func (s *hookServer) fail(event string, err error) hookOutput {
	err = fmt.Errorf("the hook failed: %w", err)
	if event == eventStop {
		return s.stopFor(eventStop, err)
	}
	return failure(event, "codex: "+err.Error())
}

// eventName returns the hook event of the input data, or "" if data
// does not name one.
func eventName(data []byte) string {
	var in struct {
		HookEventName string `json:"hook_event_name"`
	}
	_ = json.Unmarshal(data, &in)
	return in.HookEventName
}

// failure returns the answer of a failed call at event, with the text
// as its reason. The answer stops the action of the event:
//
//   - PreToolUse denies the call.
//   - PostToolUse gives the model the text in place of the output.
//   - Stop ends the turn.
//
// A call at another event gets the answer of PreToolUse.
func failure(event, text string) hookOutput {
	switch event {
	case eventPostToolUse:
		return hookOutput{
			Decision: "block",
			Reason:   text,
		}
	case eventStop:
		stop := false
		return hookOutput{
			Continue:   &stop,
			StopReason: text,
		}
	}
	return denyTool(text)
}

// reply returns the answer to the hook input data as JSON.
func (s *hookServer) reply(data []byte) ([]byte, error) {
	var in hookInput
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, err
	}
	ctx, cancel := s.callContext()
	defer cancel()
	return json.Marshal(s.answer(ctx, in))
}

// bind gives the hooks the context of the run in progress, so that the
// end of the run ends a hook. bind(nil) goes back to the context of the
// server.
func (s *hookServer) bind(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runCtx = ctx
}

// callContext returns the context of a hook call, and the function
// that releases it. The context ends when the run ends or when the
// server closes, so that Close does not wait for a hook that waits for
// a person. Between runs, it is the context of the server.
func (s *hookServer) callContext() (context.Context, context.CancelFunc) {
	s.mu.Lock()
	run := s.runCtx
	s.mu.Unlock()
	if run == nil {
		return s.ctx, func() {}
	}
	ctx, cancel := context.WithCancel(run)
	stop := context.AfterFunc(s.ctx, cancel)
	return ctx, func() {
		stop()
		cancel()
	}
}

// answer runs the hooks at the point of the event of in, in the order
// of registration.
func (s *hookServer) answer(ctx context.Context, in hookInput) hookOutput {
	call := ai.ToolCall{
		ID:    in.ToolUseID,
		Name:  toolName(in.ToolName),
		Input: in.ToolInput,
	}
	switch in.HookEventName {
	case eventPreToolUse:
		return s.beforeTool(ctx, call)
	case eventPostToolUse:
		return s.afterTool(ctx, call, hooktext.Response(in.ToolResponse))
	case eventStop:
		return s.beforeStop(ctx)
	}
	return hookOutput{}
}

// toolName returns the name of a tool of a hook as the log names it.
func toolName(name string) string {
	if name == hookBash {
		return toolBash
	}
	if rest, ok := strings.CutPrefix(name, hookMCPPrefix); ok {
		return rest
	}
	return name
}

func (s *hookServer) beforeTool(ctx context.Context, orig ai.ToolCall) hookOutput {
	call := orig
	for _, h := range s.hooks {
		bt, ok := h.(agent.BeforeToolHook)
		if !ok {
			continue
		}
		next, err := bt.BeforeTool(ctx, s.state(), call)
		if err != nil {
			return denyTool(err.Error())
		}
		call = next
	}
	if call.Name != orig.Name {
		return denyTool(renameText)
	}
	if jsonx.Same(call.Input, orig.Input) {
		return hookOutput{}
	}
	// Codex reads a new input of another shape as a failed hook, and
	// runs the original call.
	badShape := needsCommand(orig.Name) && !hasCommand(call.Input)
	if badShape {
		return denyTool(commandText)
	}
	return hookOutput{HookSpecificOutput: &hookSpecific{
		HookEventName:      eventPreToolUse,
		PermissionDecision: "allow",
		UpdatedInput:       call.Input,
	}}
}

// needsCommand reports whether Codex reads a new input of the tool name
// only as an object with a string command.
func needsCommand(name string) bool {
	return name == toolBash || name == toolApplyPatch
}

// hasCommand reports whether input is an object with a string command.
func hasCommand(input json.RawMessage) bool {
	var v struct {
		Command *string `json:"command"`
	}
	err := json.Unmarshal(input, &v)
	return err == nil && v.Command != nil
}

func denyTool(reason string) hookOutput {
	return hookOutput{HookSpecificOutput: &hookSpecific{
		HookEventName:            eventPreToolUse,
		PermissionDecision:       "deny",
		PermissionDecisionReason: reason,
	}}
}

func (s *hookServer) afterTool(ctx context.Context, call ai.ToolCall, orig string) hookOutput {
	result := hooktext.ToolResult(orig)
	for _, h := range s.hooks {
		at, ok := h.(agent.AfterToolHook)
		if !ok {
			continue
		}
		next, err := at.AfterTool(ctx, s.state(), call, result)
		if err != nil {
			return hookOutput{
				Decision: "block",
				Reason:   err.Error(),
			}
		}
		result = next
	}
	text := hooktext.Result(result)
	if text == orig {
		return hookOutput{}
	}
	return hookOutput{
		Decision: "block",
		Reason:   text,
	}
}

func (s *hookServer) beforeStop(ctx context.Context) hookOutput {
	for _, h := range s.hooks {
		bs, ok := h.(agent.BeforeStopHook)
		if !ok {
			continue
		}
		entries, err := bs.BeforeStop(ctx, s.state())
		if err != nil {
			return s.stopFor(eventStop, err)
		}
		if len(entries) == 0 {
			continue
		}
		text, err := hooktext.Entries(entries)
		if err != nil {
			return s.stopFor(eventStop, err)
		}
		return hookOutput{
			Decision: "block",
			Reason:   text,
		}
	}
	return hookOutput{}
}

// stopFor keeps the error of a hook at event, so that the run fails,
// and returns the answer that stops the turn.
func (s *hookServer) stopFor(event string, err error) hookOutput {
	s.mu.Lock()
	if s.err == nil {
		s.err = fmt.Errorf("codex: the hook at %s: %w", event, err)
	}
	s.mu.Unlock()
	return failure(eventStop, err.Error())
}

// takeErr returns the error of a hook that stopped the turn, and
// clears it.
func (s *hookServer) takeErr() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.err
	s.err = nil
	return err
}

// close ends the context of the calls in flight, stops the server,
// waits for the calls, and removes the socket.
func (s *hookServer) close() {
	s.cancel()
	_ = s.ln.Close()
	s.wg.Wait()
	_ = os.RemoveAll(s.dir)
}

// HookShim relays one hook call of the CLI to the agent that listens on
// socket: it sends the input from stdin, and writes the answer to
// stdout. "gopi hook" runs it.
//
// Codex goes on when a hook fails. So if the agent is not reachable, or
// if it closes the connection with no answer, the shim stops the
// action of the event. A PreToolUse call gets a
// deny. A PostToolUse call gives the model the error in place of the
// output. A Stop ends the turn. Another event gets no answer.
func HookShim(ctx context.Context, socket string, stdin io.Reader, stdout io.Writer) error {
	input, err := io.ReadAll(stdin)
	if err != nil {
		return fmt.Errorf("codex: read the hook input: %w", err)
	}
	answer, err := relay(ctx, socket, input)
	if err == nil {
		_, err = stdout.Write(answer)
		return err
	}

	event := eventName(input)
	known := event == eventPreToolUse || event == eventPostToolUse || event == eventStop
	if !known {
		return nil
	}
	out, merr := json.Marshal(failure(event, unreachableText+": "+err.Error()))
	if merr != nil {
		return merr
	}
	_, err = stdout.Write(out)
	return err
}

// relay sends input to the socket and returns the answer. The server
// answers every call, so an empty answer is an error: the host stopped
// while the call waited.
func relay(ctx context.Context, socket string, input []byte) ([]byte, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socket)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if _, err := conn.Write(input); err != nil {
		return nil, err
	}
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return nil, errors.New("the hook socket is not a unix socket")
	}
	if err := uc.CloseWrite(); err != nil {
		return nil, err
	}
	answer, err := io.ReadAll(conn)
	if err != nil {
		return nil, err
	}
	if len(answer) == 0 {
		return nil, errors.New("the host closed the connection with no answer")
	}
	return answer, nil
}
