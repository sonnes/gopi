package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/internal/stdiorr"
	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/internal/emittest"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
)

// policyHook is a hook at BeforeTool, AfterTool, and BeforeStop. It
// records the name of each call that it sees.
type policyHook struct {
	agent.BaseHook
	names *[]string
	stops *int
}

func (h policyHook) BeforeTool(_ context.Context, _ agent.State, c ai.ToolCall) (ai.ToolCall, error) {
	*h.names = append(*h.names, c.Name)
	if strings.Contains(string(c.Input), "rm ") {
		return c, agent.Deny("Do not delete files.")
	}
	if strings.Contains(string(c.Input), `"ls"`) {
		c.Input = json.RawMessage(`{"command":"ls -a"}`)
	}
	return c, nil
}

func (policyHook) AfterTool(
	_ context.Context,
	_ agent.State,
	_ ai.ToolCall,
	r ai.ToolResult,
) (ai.ToolResult, error) {
	text := "files: " + resultText(r)
	return ai.ToolResult{Content: []ai.Content{ai.Text{Text: text}}}, nil
}

func (h policyHook) BeforeStop(context.Context, agent.State) ([]session.Entry, error) {
	*h.stops++
	if *h.stops > 1 {
		return nil, nil
	}
	return []session.Entry{session.Text("Also say goodbye.")}, nil
}

// resultText returns the text of the blocks of r.
func resultText(r ai.ToolResult) string {
	var out string
	for _, c := range r.Content {
		if t, ok := c.(ai.Text); ok {
			out += t.Text
		}
	}
	return out
}

// failingStop is a hook at BeforeStop that fails.
type failingStop struct{ agent.BaseHook }

func (failingStop) BeforeStop(context.Context, agent.State) ([]session.Entry, error) {
	return nil, errors.New("the check failed")
}

// turnHook is a hook at AfterTurn, a point that Codex does not have.
type turnHook struct{ agent.BaseHook }

func (turnHook) AfterTurn(context.Context, agent.State) ([]session.Entry, error) {
	return nil, nil
}

// noState is the state of an agent with no runs.
func noState() agent.State { return agent.State{} }

func TestHookShim(t *testing.T) {
	var names []string
	stops := 0
	srv, err := startHookServer([]agent.Hook{policyHook{names: &names, stops: &stops}}, noState)
	require.NoError(t, err)
	defer srv.close()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "a deny blocks a command",
			input: `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"rm a.txt"},"tool_use_id":"c1"}`,
			want:  `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Do not delete files."}}`,
		},
		{
			name:  "a new input allows the call with it",
			input: `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls"},"tool_use_id":"c2"}`,
			want:  `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","updatedInput":{"command":"ls -a"}}}`,
		},
		{
			name:  "a new result replaces the output",
			input: `{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"ls"},"tool_response":"a.txt","tool_use_id":"c2"}`,
			want:  `{"decision":"block","reason":"files: a.txt"}`,
		},
		{
			name:  "a stop continues the run",
			input: `{"hook_event_name":"Stop","stop_hook_active":false}`,
			want:  `{"decision":"block","reason":"Also say goodbye."}`,
		},
		{
			name:  "an event with no point changes nothing",
			input: `{"hook_event_name":"SessionStart"}`,
			want:  `{}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			err := HookShim(context.Background(), srv.socket, strings.NewReader(tt.input), &out)
			require.NoError(t, err)
			assert.JSONEq(t, tt.want, out.String())
		})
	}
	assert.Equal(t, []string{"bash", "bash"}, names, "the hook gets the tool names of the log")
}

// silentHost returns the socket of a host that reads each hook input
// and closes the connection with no answer, as a host does when it
// stops while a hook waits.
func silentHost(t *testing.T) string {
	t.Helper()
	dir, err := hookDir()
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, hookSocket)
	ln, err := net.Listen("unix", socket)
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_, _ = io.ReadAll(conn)
			conn.Close()
		}
	}()
	return socket
}

func TestHookShimWithoutHost(t *testing.T) {
	hosts := []struct {
		name   string
		socket string
	}{
		{
			name:   "no host",
			socket: filepath.Join(t.TempDir(), "missing.sock"),
		},
		{
			name:   "a host that gives no answer",
			socket: silentHost(t),
		},
	}
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "a tool call is denied",
			input: `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls"}}`,
			want:  `"permissionDecision":"deny"`,
		},
		{
			name:  "the error replaces a tool output",
			input: `{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_response":"secret"}`,
			want:  `"decision":"block"`,
		},
		{
			name:  "a stop ends the turn",
			input: `{"hook_event_name":"Stop"}`,
			want:  `"continue":false`,
		},
		{
			name:  "an event with no point gets no answer",
			input: `{"hook_event_name":"SessionStart"}`,
		},
	}
	for _, host := range hosts {
		for _, tt := range tests {
			t.Run(host.name+"/"+tt.name, func(t *testing.T) {
				var out bytes.Buffer
				err := HookShim(context.Background(), host.socket, strings.NewReader(tt.input), &out)
				require.NoError(t, err)
				if tt.want == "" {
					assert.Empty(t, out.String())
					return
				}
				assert.Contains(t, out.String(), tt.want)
				assert.Contains(t, out.String(), "the gopi host of the hook is not reachable")
			})
		}
	}
}

func TestHookErrorStopsTheTurn(t *testing.T) {
	srv, err := startHookServer([]agent.Hook{failingStop{}}, noState)
	require.NoError(t, err)
	defer srv.close()

	var out bytes.Buffer
	input := `{"hook_event_name":"Stop"}`
	require.NoError(t, HookShim(context.Background(), srv.socket, strings.NewReader(input), &out))
	assert.JSONEq(t, `{"continue":false,"stopReason":"the check failed"}`, out.String())
	assert.EqualError(t, srv.takeErr(), "codex: the hook at Stop: the check failed")
	assert.NoError(t, srv.takeErr(), "takeErr clears the error")
}

// fakeGopi returns the path of an executable file that stands in for
// the gopi binary, and a scrub that gives the path the name /bin/gopi.
func fakeGopi(t *testing.T) (string, stdiorr.Scrub) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gopi")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"), 0o700))
	return path, replaceScrub(path, "/bin/gopi")
}

// socketScrub gives the hook socket of a thread a fixed path.
var socketScrub = func(line []byte) []byte {
	re := regexp.MustCompile(`'[^']*/gopi-hook-[^/']*/hook\.sock'`)
	return re.ReplaceAll(line, []byte(`'/tmp/gopi-hook/hook.sock'`))
}

func TestThreadStartAddsTheHooks(t *testing.T) {
	gopi, gopiScrub := fakeGopi(t)
	useTraces(t, []string{"hook_thread.trace"}, gopiScrub, socketScrub)
	var names []string
	stops := 0
	a := newAgent(t,
		Hooks(policyHook{names: &names, stops: &stops}),
		HookCommand(gopi),
	)

	_, msgs, err := collect(a.Run(context.Background(), session.Text("Say hello.")))
	require.NoError(t, err, "thread/start has the hooks of the agent")
	require.Len(t, msgs, 1)
}

func TestRelativeHookCommandBecomesAbsolute(t *testing.T) {
	gopi, gopiScrub := fakeGopi(t)
	wd, err := os.Getwd()
	require.NoError(t, err)
	relative, err := filepath.Rel(wd, gopi)
	require.NoError(t, err)
	useTraces(t, []string{"hook_thread.trace"}, gopiScrub, socketScrub)
	stops := 0
	a := newAgent(t,
		Hooks(policyHook{names: new([]string), stops: &stops}),
		HookCommand(relative),
	)

	_, _, err = collect(a.Run(context.Background(), session.Text("Say hello.")))
	require.NoError(t, err, "Codex runs the hook from the thread directory, so the path must be absolute")
}

func TestHookAtAMissingPointFails(t *testing.T) {
	useTraces(t, nil)
	a := newAgent(t, Hooks(turnHook{}))

	_, _, err := collect(a.Run(context.Background(), session.Text("Say hello.")))
	assert.ErrorIs(t, err, errors.ErrUnsupported)
}

// userHookScrub empties the entries of a hook of the user config, such
// as the context that a SessionStart hook of the machine adds.
func userHookScrub(line []byte) []byte {
	var m struct {
		Method string         `json:"method"`
		Params map[string]any `json:"params"`
	}
	if err := json.Unmarshal(line, &m); err != nil || !strings.HasPrefix(m.Method, "hook/") {
		return line
	}
	run, ok := m.Params["run"].(map[string]any)
	if !ok || run["source"] != "user" {
		return line
	}
	run["entries"] = []any{}
	var full map[string]any
	if err := json.Unmarshal(line, &full); err != nil {
		return line
	}
	full["params"] = m.Params
	out, err := json.Marshal(full)
	if err != nil {
		return line
	}
	return out
}

// TestRecordedHooks runs the real CLI with hooks that the gopi binary
// relays. Build the binary with `make build`, then record the test with
// `make record-cli AGENT=codex RUN=TestRecordedHooks`.
func TestRecordedHooks(t *testing.T) {
	dir := t.TempDir()
	real, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	// The CLI runs the hooks out of band, through the gopi binary. So a
	// replay runs no hook, and only a recording needs the real binary
	// and sees the calls.
	recording, err := stdiorr.Recording(filepath.Join("testdata", "TestRecordedHooks.trace"))
	require.NoError(t, err)
	gopi, gopiScrub := fakeGopi(t)
	if recording {
		gopi, err = filepath.Abs("../../../.bin/gopi")
		require.NoError(t, err)
		gopiScrub = replaceScrub(gopi, "/bin/gopi")
	}
	useTraces(t, []string{"TestRecordedHooks.trace"},
		gopiScrub,
		socketScrub,
		replaceScrub(jsonString(real), workDir),
		replaceScrub(jsonString(dir), workDir),
		replaceScrub(jsonString(home), "/home/me"),
		machineScrub,
		userHookScrub,
	)
	var names []string
	stops := 0
	a := New(
		Dir(dir),
		Policy(ApprovalNever),
		Thinking(ai.ThinkingLow),
		Hooks(policyHook{names: &names, stops: &stops}),
		HookCommand(gopi),
	)
	t.Cleanup(func() { a.Close() })

	prompt := "Run the shell command `rm a.txt`. Then reply with the word done."
	events, _, err := collect(a.Run(context.Background(), session.Text(prompt)))
	require.NoError(t, err)
	emittest.CheckEvents(t, events)
	checkLog(t, a)
	if recording {
		assert.Contains(t, names, "bash", "the CLI calls the PreToolUse hook")
		assert.Equal(t, 2, stops, "the first Stop continues the turn")
	}
}

// badInput is a hook at BeforeTool that gives an input that is not JSON.
type badInput struct{ agent.BaseHook }

func (badInput) BeforeTool(_ context.Context, _ agent.State, c ai.ToolCall) (ai.ToolCall, error) {
	c.Input = json.RawMessage("{not json")
	return c, nil
}

func TestHookServerFailure(t *testing.T) {
	// Codex goes on when a hook gives no answer, so each failure gives
	// the answer of its event that stops the action.
	tests := []struct {
		name    string
		input   string
		want    string
		failRun bool
	}{
		{
			name:  "a tool call is denied",
			input: `{"hook_event_name":"PreToolUse","tool_name":"mcp__docs__search","tool_input":{"query":"a"}}`,
			want:  `"permissionDecision":"deny"`,
		},
		{
			name:  "the error replaces a tool output",
			input: `{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_use_id":5}`,
			want:  `"decision":"block"`,
		},
		{
			name:    "a stop ends the turn and fails the run",
			input:   `{"hook_event_name":"Stop","tool_use_id":5}`,
			want:    `"continue":false`,
			failRun: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, err := startHookServer([]agent.Hook{badInput{}}, noState)
			require.NoError(t, err)
			defer srv.close()

			var out bytes.Buffer
			require.NoError(t, HookShim(context.Background(), srv.socket, strings.NewReader(tt.input), &out))
			assert.Contains(t, out.String(), tt.want)
			assert.Equal(t, tt.failRun, srv.takeErr() != nil)
		})
	}
}

// waitHook is a hook at BeforeTool that waits until its context ends,
// as a hook that asks a person does.
type waitHook struct {
	agent.BaseHook
	entered chan struct{}
}

func (h waitHook) BeforeTool(ctx context.Context, _ agent.State, c ai.ToolCall) (ai.ToolCall, error) {
	close(h.entered)
	<-ctx.Done()
	return c, ctx.Err()
}

// hookSocketOf returns the hook socket of the process of a, when the
// process runs.
func hookSocketOf(t *testing.T, a *Agent) string {
	t.Helper()
	var socket string
	require.Eventually(t, func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.conn == nil || a.conn.hooks == nil {
			return false
		}
		socket = a.conn.hooks.socket
		return true
	}, closeTimeout, time.Millisecond)
	return socket
}

// closeTimeout is how long a test waits for Close.
const closeTimeout = 5 * time.Second

func TestCloseWhileAHookWaits(t *testing.T) {
	gopi, gopiScrub := fakeGopi(t)
	useTraces(t, []string{"hook_wait.trace"}, gopiScrub, socketScrub)
	entered := make(chan struct{})
	a := newAgent(t,
		Hooks(waitHook{entered: entered}),
		HookCommand(gopi),
	)

	s := a.Run(context.Background(), session.Text("Run ls."))
	socket := hookSocketOf(t, a)
	input := `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls"},"tool_use_id":"c1"}`
	var out bytes.Buffer
	shim := make(chan error, 1)
	go func() {
		shim <- HookShim(context.Background(), socket, strings.NewReader(input), &out)
	}()
	<-entered

	closed := make(chan error, 1)
	go func() { closed <- a.Close() }()
	select {
	case <-closed:
	case <-time.After(closeTimeout):
		t.Fatal("Close waits for a hook that waits for its context")
	}
	require.NoError(t, <-shim)
	assert.Contains(t, out.String(), `"permissionDecision":"deny"`, "the hook ends with the agent")
	_, err := s.Wait()
	assert.Error(t, err, "the run ends with the process")
}

func TestMissingHookCommandFails(t *testing.T) {
	tests := []struct {
		name string
		opts []Option
	}{
		{
			name: "the default command is not on the PATH",
		},
		{
			name: "the command path does not exist",
			opts: []Option{HookCommand(filepath.Join(t.TempDir(), "gopi"))},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("PATH", t.TempDir())
			useTraces(t, nil)
			opts := append([]Option{Hooks(policyHook{})}, tt.opts...)
			a := newAgent(t, opts...)

			_, _, err := collect(a.Run(context.Background(), session.Text("Say hello.")))
			require.Error(t, err, "Codex runs the tool when the hook command is missing")
			var execErr *exec.Error
			assert.ErrorAs(t, err, &execErr)
			assert.ErrorContains(t, err, "codex: find the hook command")
		})
	}
}

// inputHook is a hook at BeforeTool that gives the call the input
// input.
type inputHook struct {
	agent.BaseHook
	input string
}

func (h inputHook) BeforeTool(_ context.Context, _ agent.State, c ai.ToolCall) (ai.ToolCall, error) {
	c.Input = json.RawMessage(h.input)
	return c, nil
}

func TestHookNewInput(t *testing.T) {
	const shapeDeny = `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny",` +
		`"permissionDecisionReason":"codex: a hook must give Bash and apply_patch an input with a string command"}}`
	tests := []struct {
		name  string
		tool  string
		input string
		hook  string
		want  string
	}{
		{
			name:  "the same JSON in another form changes nothing",
			tool:  "Bash",
			input: `{"command":"ls","timeout":5}`,
			hook:  "{ \"timeout\": 5,\n  \"command\": \"ls\" }",
			want:  `{}`,
		},
		{
			name:  "a Bash input with no command is denied",
			tool:  "Bash",
			input: `{"command":"ls"}`,
			hook:  `{"cmd":"ls -a"}`,
			want:  shapeDeny,
		},
		{
			name:  "an apply_patch input with a command that is not a string is denied",
			tool:  "apply_patch",
			input: `{"command":"*** Begin Patch"}`,
			hook:  `{"command":["*** Begin Patch"]}`,
			want:  shapeDeny,
		},
		{
			name:  "an apply_patch input with a string command is allowed",
			tool:  "apply_patch",
			input: `{"command":"*** Begin Patch"}`,
			hook:  `{"command":"*** Begin Patch\n"}`,
			want:  `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","updatedInput":{"command":"*** Begin Patch\n"}}}`,
		},
		{
			name:  "an MCP input is the new arguments",
			tool:  "mcp__docs__search",
			input: `{"query":"a"}`,
			hook:  `{"query":"b"}`,
			want:  `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","updatedInput":{"query":"b"}}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, err := startHookServer([]agent.Hook{inputHook{input: tt.hook}}, noState)
			require.NoError(t, err)
			defer srv.close()

			in, err := json.Marshal(map[string]any{
				"hook_event_name": "PreToolUse",
				"tool_name":       tt.tool,
				"tool_input":      json.RawMessage(tt.input),
			})
			require.NoError(t, err)
			var out bytes.Buffer
			require.NoError(t, HookShim(context.Background(), srv.socket, bytes.NewReader(in), &out))
			assert.JSONEq(t, tt.want, out.String())
		})
	}
}

func TestHookServerEndsAStuckConnection(t *testing.T) {
	saved := hookReadTimeout
	hookReadTimeout = 50 * time.Millisecond
	t.Cleanup(func() { hookReadTimeout = saved })
	srv, err := startHookServer([]agent.Hook{policyHook{}}, noState)
	require.NoError(t, err)
	defer srv.close()

	conn, err := net.Dial("unix", srv.socket)
	require.NoError(t, err)
	defer conn.Close()
	_, err = conn.Write([]byte(`{"hook_event_name":"PreToolUse"`))
	require.NoError(t, err)
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(closeTimeout)))

	out, err := io.ReadAll(conn)
	require.NoError(t, err, "the server ends a connection whose input does not end")
	assert.Contains(t, string(out), `"permissionDecision":"deny"`)
}

func TestHookServerWithALongTempDir(t *testing.T) {
	long := filepath.Join(t.TempDir(), strings.Repeat("d", 100))
	require.NoError(t, os.MkdirAll(long, 0o700))
	t.Setenv("TMPDIR", long)

	srv, err := startHookServer([]agent.Hook{policyHook{}}, noState)
	require.NoError(t, err, "a socket path over the limit of the OS fails to listen")
	defer srv.close()

	var out bytes.Buffer
	input := `{"hook_event_name":"SessionStart"}`
	require.NoError(t, HookShim(context.Background(), srv.socket, strings.NewReader(input), &out))
	assert.JSONEq(t, `{}`, out.String())
}
