package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/internal/clihook"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/tool"
)

// hookEvents maps each point of a gopi hook that the CLI calls to the
// hook event of the CLI at that point. The CLI has no hook at a model
// call and no hook after each turn.
var hookEvents = map[agent.Point]string{
	agent.PointBeforeTool: clihook.EventPreToolUse,
	agent.PointAfterTool:  clihook.EventPostToolUse,
	agent.PointBeforeStop: clihook.EventStop,
}

// checkHooks returns an error for a hook at a point that the CLI does
// not call, or for a hook that implements no point.
func checkHooks(hooks []agent.Hook) error {
	points := slices.Collect(maps.Keys(hookEvents))
	if err := agent.CheckHooks(hooks, points...); err != nil {
		return fmt.Errorf("codex: %w", err)
	}
	return nil
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
	// Timeout is the time limit of the command, in seconds. Zero keeps
	// the default limit of the CLI.
	Timeout int `json:"timeout,omitempty"`
}

// hookGroup is the list of handlers of one hook event.
type hookGroup struct {
	Hooks []hookHandler `json:"hooks"`
}

// hookConfig returns the config of thread/start that adds a command
// hook for each event that a hook uses. The command relays the hook to
// socket. PreToolUse gets [clihook.AskTimeout], because a BeforeTool
// hook can wait for a person.
func hookConfig(command, socket string, hooks []agent.Hook) map[string]any {
	line := shellQuote(command) + " hook " + shellQuote(socket)
	events := make(map[string][]hookGroup)
	for _, h := range hooks {
		for _, p := range agent.Points(h) {
			event := hookEvents[p]
			handler := hookHandler{
				Type:    "command",
				Command: line,
			}
			if event == clihook.EventPreToolUse {
				handler.Timeout = clihook.AskTimeout
			}
			events[event] = []hookGroup{{Hooks: []hookHandler{handler}}}
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

// shellQuote quotes s for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// hookServer answers the hook calls of one process of the CLI on a unix
// socket. Each connection carries one call: the input, then the answer.
type hookServer struct {
	ln     net.Listener
	dir    string
	socket string
	hooks  []agent.Hook
	state  func() agent.State
	// approve answers a hook that returns an [agent.AskError].
	approve agent.ApproveFunc
	// stops keeps the error of a hook that stopped the turn.
	stops *clihook.StopErr
	// ctx ends when the server closes. A hook between runs gets it. A
	// hook in a run gets a context that ends with the run or with ctx.
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu sync.Mutex
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
// hooks. approve answers a hook that asks a person. A hook that stops
// the turn keeps its error in stops.
func startHookServer(
	hooks []agent.Hook,
	state func() agent.State,
	approve agent.ApproveFunc,
	stops *clihook.StopErr,
) (*hookServer, error) {
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
		ln:      ln,
		dir:     dir,
		socket:  socket,
		hooks:   hooks,
		state:   state,
		approve: approve,
		stops:   stops,
		ctx:     ctx,
		cancel:  cancel,
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
		s.wg.Go(func() {
			s.handle(conn)
		})
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
func (s *hookServer) fail(event string, err error) clihook.Output {
	err = fmt.Errorf("the hook failed: %w", err)
	if event == clihook.EventStop {
		return s.stops.Stop(clihook.EventStop, err)
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
func failure(event, text string) clihook.Output {
	switch event {
	case clihook.EventPostToolUse:
		return clihook.Block(text)
	case clihook.EventStop:
		return clihook.Stop(text)
	}
	return clihook.Deny(text)
}

// reply returns the answer to the hook input data as JSON.
func (s *hookServer) reply(data []byte) ([]byte, error) {
	var in clihook.Input
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
func (s *hookServer) answer(ctx context.Context, in clihook.Input) clihook.Output {
	switch in.HookEventName {
	case clihook.EventPreToolUse:
		return s.beforeTool(ctx, in.Call())
	case clihook.EventPostToolUse:
		return s.afterTool(ctx, in.Call(), clihook.Response(in.ToolResponse))
	case clihook.EventStop:
		return clihook.BeforeStop(ctx, s.hooks, s.state, s.stops)
	}
	return clihook.Output{}
}

// beforeTool runs the BeforeTool hooks on orig, and returns the answer
// at PreToolUse.
func (s *hookServer) beforeTool(ctx context.Context, orig ai.ToolCall) clihook.Output {
	call, err := agent.RunBeforeTool(
		ctx,
		s.hooks,
		s.state,
		s.approve,
		orig,
	)
	if err != nil {
		return clihook.Deny(err.Error())
	}
	if call.Name != orig.Name {
		return clihook.Deny(renameText)
	}
	if clihook.SameInput(call.Input, orig.Input) {
		return clihook.Output{}
	}
	// Codex reads a new input of another shape as a failed hook, and
	// runs the original call.
	badShape := needsCommand(orig.Name) && !hasCommand(call.Input)
	if badShape {
		return clihook.Deny(commandText)
	}
	return clihook.Output{HookSpecificOutput: &clihook.Specific{
		HookEventName:      clihook.EventPreToolUse,
		PermissionDecision: "allow",
		UpdatedInput:       call.Input,
	}}
}

// needsCommand reports whether Codex reads a new input of the tool name
// only as an object with a string command.
func needsCommand(name string) bool {
	return name == tool.Bash || name == tool.ApplyPatch
}

// hasCommand reports whether input is an object with a string command.
func hasCommand(input json.RawMessage) bool {
	var v struct {
		Command *string `json:"command"`
	}
	err := json.Unmarshal(input, &v)
	return err == nil && v.Command != nil
}

func (s *hookServer) afterTool(ctx context.Context, call ai.ToolCall, orig string) clihook.Output {
	result, err := agent.RunAfterTool(
		ctx,
		s.hooks,
		s.state,
		call,
		clihook.ToolResult(orig),
	)
	if err != nil {
		return clihook.Block(err.Error())
	}
	text := clihook.Result(result)
	if text == orig {
		return clihook.Output{}
	}
	return clihook.Block(text)
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
	known := event == clihook.EventPreToolUse || event == clihook.EventPostToolUse || event == clihook.EventStop
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
