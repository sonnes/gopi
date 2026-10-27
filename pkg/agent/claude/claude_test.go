package claude_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/claude"
	"github.com/sonnes/gopi/pkg/agent/internal/emittest"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
)

// run runs a with input and returns the events, the result, and the
// error of the stream. A failed stream still has a result.
func run(ctx context.Context, a *claude.Agent, input ...string) ([]agent.Event, []ai.Message, error) {
	entries := make([]session.Entry, 0, len(input))
	for _, s := range input {
		entries = append(entries, session.Text(s))
	}
	s := a.Run(ctx, entries...)
	var events []agent.Event
	for ev, err := range s.Events() {
		if err != nil {
			break
		}
		events = append(events, ev)
	}
	msgs, err := s.Wait()
	return events, msgs, err
}

// checkLog fails t if a tool call in the log of a has no result, or if
// the state of a does not hold the messages of the log. A custom entry
// holds no message.
func checkLog(t *testing.T, a *claude.Agent) {
	t.Helper()
	log := a.Log()
	var entries []ai.Message
	for _, e := range log.Entries() {
		if e.Kind != "" {
			continue
		}
		entries = append(entries, e.Message)
	}
	assert.Equal(t, entries, log.Messages(), "every tool call in the log has a result")
	assert.Equal(t, log.Messages(), a.State().Messages(), "the state has the messages of the log")
	assert.False(t, a.State().Running())
}

// lastEnd returns the agent_end of a run.
func lastEnd(t *testing.T, events []agent.Event) agent.EventAgentEnd {
	t.Helper()
	require.NotEmpty(t, events)
	end, ok := events[len(events)-1].(agent.EventAgentEnd)
	require.True(t, ok, "the last event is agent_end, not %T", events[len(events)-1])
	return end
}

func closeAgent(t *testing.T, a *claude.Agent) {
	t.Helper()
	require.NoError(t, a.Close())
}

func TestRunText(t *testing.T) {
	claude.Replay(t, nil, "testdata/text.trace")
	a := claude.New(claude.Name("claude"))
	defer closeAgent(t, a)

	events, msgs, err := run(context.Background(), a, "Say hello")
	require.NoError(t, err)
	emittest.CheckEvents(t, events)

	assert.Equal(t, []string{
		"agent_start",
		"turn_start",
		"message_start", // input
		"message_end",
		"message_start",
		"message_update",
		"message_update",
		"message_update",
		"message_update",
		"message_end",
		"turn_end",
		"agent_end",
	}, emittest.Types(events))

	require.Len(t, msgs, 1)
	assert.Equal(t, []ai.Content{ai.Text{Text: "Hello there"}}, msgs[0].Content)
	assert.Equal(t, ai.StopReasonStop, msgs[0].StopReason)
	assert.Equal(t, "claude-haiku-4-5", msgs[0].Model)
	assert.Equal(t, ai.Usage{Input: 10, Output: 5, CacheRead: 20, CacheWrite: 30}, msgs[0].Usage)

	end := lastEnd(t, events)
	assert.Equal(t, ai.Usage{Input: 10, Output: 5, CacheRead: 20, CacheWrite: 30}, end.Usage)

	entries := a.Log().Entries()
	require.Len(t, entries, 2)
	assert.Equal(t, "Say hello", entries[0].Message.Text())
	assert.Equal(t, "claude", entries[1].Author)
	checkLog(t, a)

	id, err := claude.SessionID(a)
	require.NoError(t, err)
	assert.Equal(t, "sess-1", id)
}

func TestRunDropsOtherUserText(t *testing.T) {
	claude.Replay(t, nil, "testdata/user_text.trace")
	a := claude.New()
	defer closeAgent(t, a)

	events, _, err := run(context.Background(), a, "Say hello")
	require.NoError(t, err)
	emittest.CheckEvents(t, events)

	var log []string
	for _, e := range a.Log().Entries() {
		log = append(log, string(e.Message.Role)+": "+e.Message.Text())
	}
	assert.Equal(t,
		[]string{
			"user: Say hello",
			"assistant: Hello there",
		},
		log,
		"the log keeps only the Stop hook feedback of the user lines of the CLI",
	)
	checkLog(t, a)
}

func TestRunThinking(t *testing.T) {
	claude.Replay(t, nil, "testdata/thinking.trace")
	a := claude.New()
	defer closeAgent(t, a)

	events, msgs, err := run(context.Background(), a, "Think, then say hi")
	require.NoError(t, err)
	emittest.CheckEvents(t, events)

	require.Len(t, msgs, 1)
	assert.Equal(t, []ai.Content{
		ai.ThinkingBlock{Thinking: "The user wants hi.", Signature: "sig-1"},
		ai.Text{Text: "hi"},
	}, msgs[0].Content)

	var deltas string
	for _, ev := range events {
		up, ok := ev.(agent.EventMessageUpdate)
		if ok && up.AssistantEvent.Type == ai.EventThinkDelta {
			deltas += up.AssistantEvent.Delta
		}
	}
	assert.Equal(t, "The user wants hi.", deltas)
	checkLog(t, a)
}

func TestRunToolCall(t *testing.T) {
	tests := []struct {
		name      string
		file      string
		result    string
		isError   bool
		finalText string
	}{
		{
			name:      "success",
			file:      "testdata/tool.trace",
			result:    "hi",
			finalText: "It printed hi.",
		},
		{
			name:      "error",
			file:      "testdata/tool_error.trace",
			result:    "command not found",
			isError:   true,
			finalText: "It failed.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claude.Replay(t, nil, tt.file)
			a := claude.New()
			defer closeAgent(t, a)

			events, msgs, err := run(context.Background(), a, "Run echo hi")
			require.NoError(t, err)
			emittest.CheckEvents(t, events)

			require.Len(t, msgs, 3, "the tool call, its result, and the answer")
			call := ai.ToolCall{
				ID:    "toolu_1",
				Name:  "Bash",
				Input: []byte(`{"command":"echo hi"}`),
			}
			assert.Equal(t, []ai.Content{ai.Text{Text: "Running it."}, call}, msgs[0].Content)
			assert.Equal(t, ai.StopReasonToolUse, msgs[0].StopReason)

			assert.Equal(t, ai.RoleToolResult, msgs[1].Role)
			assert.Equal(t, "toolu_1", msgs[1].ToolCallID)
			assert.Equal(t, tt.isError, msgs[1].IsError)
			assert.Equal(t, tt.result, msgs[1].Text())

			assert.Equal(t, tt.finalText, msgs[2].Text())

			var (
				start *agent.EventToolExecutionStart
				end   *agent.EventToolExecutionEnd
			)
			for _, ev := range events {
				switch ev := ev.(type) {
				case agent.EventToolExecutionStart:
					start = &ev
				case agent.EventToolExecutionEnd:
					end = &ev
				}
			}
			require.NotNil(t, start)
			require.NotNil(t, end)
			assert.Equal(t, "Bash", start.ToolName)
			assert.JSONEq(t, `{"command":"echo hi"}`, string(start.Args))
			assert.Equal(t, tt.isError, end.Result.IsError)

			assert.Len(t, a.Log().Entries(), 4)
			checkLog(t, a)
		})
	}
}

func TestRunTwiceOnOneProcess(t *testing.T) {
	starts := claude.Replay(t, nil, "testdata/two_runs.trace")
	a := claude.New()
	defer closeAgent(t, a)

	for _, in := range []string{"Say hello", "Again"} {
		events, _, err := run(context.Background(), a, in)
		require.NoError(t, err)
		emittest.CheckEvents(t, events)
	}

	assert.Len(t, starts.Cmds(), 1, "the runs share one process")
	msgs := a.Log().Messages()
	require.Len(t, msgs, 4)
	assert.Equal(t, "Hello again", msgs[3].Text())
	checkLog(t, a)
}

func TestDurableResumes(t *testing.T) {
	ctx := context.Background()
	store := session.NewMemoryStore()
	starts := claude.Replay(t, nil, "testdata/text.trace", "testdata/resume.trace")

	first, err := claude.Durable(ctx, "s1", claude.Store(store))
	require.NoError(t, err)
	events, _, err := run(ctx, first, "Say hello")
	require.NoError(t, err)
	emittest.CheckEvents(t, events)
	require.NoError(t, first.Close())

	second, err := claude.Durable(ctx, "s1", claude.Store(store))
	require.NoError(t, err)
	defer closeAgent(t, second)
	assert.Len(t, second.State().Messages(), 2, "the state starts with the stored messages")
	events, _, err = run(ctx, second, "Again")
	require.NoError(t, err)
	emittest.CheckEvents(t, events)

	cmds := starts.Cmds()
	require.Len(t, cmds, 2)
	assert.False(t, slices.ContainsFunc(cmds[0].Args, func(s string) bool {
		return strings.HasPrefix(s, "--resume")
	}), "a new session does not resume")
	assert.Contains(t, cmds[1].Args, "--resume=sess-1")

	msgs := second.Log().Messages()
	require.Len(t, msgs, 4, "the second agent continues the stored log")
	checkLog(t, second)
}

func TestRunCancel(t *testing.T) {
	claude.Replay(t, nil, "testdata/cancel.trace")
	a := claude.New()
	defer closeAgent(t, a)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := a.Run(ctx, session.Text("Count to 300"))
	var (
		events []agent.Event
		runErr error
	)
	for ev, err := range s.Events() {
		if err != nil {
			runErr = err
			break
		}
		events = append(events, ev)
		if _, ok := ev.(agent.EventMessageUpdate); ok {
			cancel()
		}
	}
	assert.ErrorIs(t, runErr, context.Canceled)
	emittest.CheckEvents(t, events)
	assert.Equal(t, context.Canceled.Error(), lastEnd(t, events).Error)

	entries := a.Log().Entries()
	require.Len(t, entries, 2, "the block that the CLI completed is stored")
	cut := entries[1].Message
	assert.Equal(t, "1\n2\n3", cut.Text())
	assert.Equal(t, ai.StopReasonAborted, cut.StopReason)
	checkLog(t, a)
}

func TestRunCancelWhileToolRuns(t *testing.T) {
	claude.Replay(t, nil, "testdata/cancel_tool.trace")
	a := claude.New()
	defer closeAgent(t, a)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := a.Run(ctx, session.Text("Run sleep 60"))
	var events []agent.Event
	for ev, err := range s.Events() {
		if err != nil {
			break
		}
		events = append(events, ev)
		if _, ok := ev.(agent.EventToolExecutionStart); ok {
			cancel()
		}
	}
	msgs, err := s.Wait()
	assert.ErrorIs(t, err, context.Canceled)
	emittest.CheckEvents(t, events)

	require.Len(t, msgs, 2, "the tool call and its real result")
	assert.Equal(t, ai.RoleToolResult, msgs[1].Role)
	assert.True(t, msgs[1].IsError)
	assert.Equal(t, "Interrupted by user", msgs[1].Text())
	assert.Len(t, a.Log().Entries(), 3)
	checkLog(t, a)
}

func TestRunErrorResult(t *testing.T) {
	claude.Replay(t, nil, "testdata/error.trace")
	a := claude.New()
	defer closeAgent(t, a)

	events, _, err := run(context.Background(), a, "Say hello")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "error_during_execution")
	assert.Contains(t, err.Error(), "API Error: 500 overloaded")
	emittest.CheckEvents(t, events)
	checkLog(t, a)
}

func TestRunFailureKeepsProduced(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		wantErr string
	}{
		{
			name:    "error result",
			file:    "testdata/tool_then_error.trace",
			wantErr: "error_max_turns",
		},
		{
			name:    "the CLI exits",
			file:    "testdata/tool_then_exit.trace",
			wantErr: "the CLI exited",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claude.Replay(t, nil, tt.file)
			a := claude.New()
			defer closeAgent(t, a)

			events, msgs, err := run(context.Background(), a, "Run echo hi")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			emittest.CheckEvents(t, events)

			require.Len(t, msgs, 2, "the stored tool call and its result")
			assert.NotZero(t, lastEnd(t, events).Usage, "agent_end keeps the usage of the run")
			assert.Equal(t, ai.StopReasonToolUse, msgs[0].StopReason)
			assert.Equal(t, ai.RoleToolResult, msgs[1].Role)
			assert.Equal(t, "hi", msgs[1].Text())
			assert.Len(t, a.Log().Entries(), 3)
			checkLog(t, a)
		})
	}
}

// updates returns the snapshots of the message_update events.
func updates(events []agent.Event) []ai.Message {
	var out []ai.Message
	for _, ev := range events {
		if up, ok := ev.(agent.EventMessageUpdate); ok {
			out = append(out, up.Message)
		}
	}
	return out
}

func TestRunSkippedBlock(t *testing.T) {
	claude.Replay(t, nil, "testdata/skipped_block.trace")
	a := claude.New()
	defer closeAgent(t, a)

	events, msgs, err := run(context.Background(), a, "Say hi")
	require.NoError(t, err)
	emittest.CheckEvents(t, events)

	require.Len(t, msgs, 1)
	assert.Equal(t, []ai.Content{ai.Text{Text: "hi"}}, msgs[0].Content)
	for _, ev := range events {
		if up, ok := ev.(agent.EventMessageUpdate); ok {
			assert.Zero(t, up.AssistantEvent.ContentIndex, "the index counts the kept blocks")
		}
	}
	snaps := updates(events)
	require.NotEmpty(t, snaps)
	assert.Equal(t, msgs[0].Content, snaps[len(snaps)-1].Content, "the partial matches the final content")
}

func TestRunEmptyMessage(t *testing.T) {
	claude.Replay(t, nil, "testdata/empty_message.trace")
	a := claude.New()
	defer closeAgent(t, a)

	events, msgs, err := run(context.Background(), a, "Say hi")
	require.NoError(t, err)
	emittest.CheckEvents(t, events)

	require.Len(t, msgs, 1, "the empty message is not stored")
	assert.Equal(t, []ai.Content{ai.Text{Text: "hi"}}, msgs[0].Content)

	var starts, ends int
	for _, ev := range events {
		switch ev.(type) {
		case agent.EventMessageStart:
			starts++
		case agent.EventMessageEnd:
			ends++
		}
	}
	assert.Equal(t, 3, starts, "the input and each API message have their own message_start")
	assert.Equal(t, 3, ends)

	snaps := updates(events)
	require.NotEmpty(t, snaps)
	assert.Equal(t, msgs[0].Content, snaps[len(snaps)-1].Content)
	assert.Len(t, a.Log().Entries(), 2)
	checkLog(t, a)
}

func TestControlRequests(t *testing.T) {
	tests := []struct {
		name  string
		file  string
		input string
		want  int
	}{
		{
			name:  "can_use_tool is denied without an approver",
			file:  "testdata/can_use_tool.trace",
			input: "Delete a.txt",
			want:  3,
		},
		{
			name:  "an unknown request gets an error",
			file:  "testdata/unknown_request.trace",
			input: "Say hello",
			want:  1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claude.Replay(t, nil, tt.file)
			a := claude.New()
			defer closeAgent(t, a)

			events, msgs, err := run(context.Background(), a, tt.input)
			require.NoError(t, err)
			emittest.CheckEvents(t, events)
			assert.Len(t, msgs, tt.want)
			checkLog(t, a)
		})
	}
}

func TestRunWithoutText(t *testing.T) {
	custom, err := session.Custom("note", "for the app")
	require.NoError(t, err)
	tests := []struct {
		name    string
		entries []session.Entry
	}{
		{
			name: "no entry",
		},
		{
			name:    "a custom entry",
			entries: []session.Entry{custom},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			starts := claude.Replay(t, nil)
			a := claude.New()
			defer closeAgent(t, a)

			_, err := a.Run(context.Background(), tt.entries...).Wait()
			assert.ErrorContains(t, err, "a run needs an entry with text")
			assert.Empty(t, starts.Cmds(), "a run without text starts no process")
			assert.Empty(t, a.Log().Entries())
		})
	}
}

func TestRunLostSession(t *testing.T) {
	ctx := context.Background()
	store := session.NewMemoryStore()
	starts := claude.Replay(t, nil,
		"testdata/text.trace",
		"testdata/lost_session.trace",
		"testdata/text.trace",
	)
	first, err := claude.Durable(ctx, "s1", claude.Store(store))
	require.NoError(t, err)
	_, _, err = run(ctx, first, "Say hello")
	require.NoError(t, err)
	require.NoError(t, first.Close())

	second, err := claude.Durable(ctx, "s1", claude.Store(store))
	require.NoError(t, err)
	defer closeAgent(t, second)
	events, _, err := run(ctx, second, "Say hello")
	require.ErrorIs(t, err, claude.ErrNoConversation)
	emittest.CheckEvents(t, events)
	id, err := claude.SessionID(second)
	require.NoError(t, err)
	assert.Empty(t, id, "the agent forgets the lost session")

	_, _, err = run(ctx, second, "Say hello")
	require.NoError(t, err, "the next run starts a new session")
	cmds := starts.Cmds()
	require.Len(t, cmds, 3)
	assert.Contains(t, cmds[1].Args, "--resume=sess-1")
	assert.False(t, slices.ContainsFunc(cmds[2].Args, func(s string) bool {
		return strings.HasPrefix(s, "--resume")
	}), "the new session does not resume")
	checkLog(t, second)
}

func TestRunWhileRunning(t *testing.T) {
	claude.Replay(t, nil, "testdata/cancel.trace")
	a := claude.New()
	defer closeAgent(t, a)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := a.Run(ctx, session.Text("Count to 300"))
	for ev, err := range first.Events() {
		require.NoError(t, err)
		if _, ok := ev.(agent.EventMessageUpdate); ok {
			break
		}
	}

	_, _, err := run(context.Background(), a, "Say hello")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "a run is active")

	cancel()
	_, err = first.Wait()
	assert.ErrorIs(t, err, context.Canceled)
}

func TestOptions(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("NODE_OPTIONS", "--inspect")
	t.Setenv("DEBUG", "1")
	starts := claude.Replay(t, nil, "testdata/text.trace")
	a := claude.New(
		claude.Binary("/opt/claude"),
		claude.Dir("/work"),
		claude.Model("haiku"),
		claude.System("Be brief."),
		claude.Thinking(ai.ThinkingHigh),
		claude.PermissionMode(claude.PermissionAcceptEdits),
		claude.AllowedTools("Read", "Bash(git:*)"),
		claude.DisallowedTools("WebFetch"),
		claude.AskTools("Bash(gh pr comment:*)"),
		claude.MCP(agent.MCPServer{
			Name:    "lw",
			Type:    agent.MCPHTTP,
			URL:     "http://127.0.0.1:1/mcp",
			Headers: map[string]string{"Authorization": "Bearer t"},
		}),
		claude.Env("FOO=bar"),
	)
	defer closeAgent(t, a)

	_, _, err := run(context.Background(), a, "Say hello")
	require.NoError(t, err)

	cmds := starts.Cmds()
	require.Len(t, cmds, 1)
	cmd := cmds[0]
	assert.Equal(t, []string{
		"/opt/claude",
		"--print",
		"--output-format", "stream-json",
		"--verbose",
		"--input-format", "stream-json",
		"--include-partial-messages",
		"--replay-user-messages",
		"--permission-prompt-tool", "stdio",
		"--model", "haiku",
		"--append-system-prompt", "Be brief.",
		"--effort", "high",
		"--permission-mode", "acceptEdits",
		"--allowedTools", "Read,Bash(git:*)",
		"--disallowedTools", "WebFetch",
		"--settings", `{"permissions":{"ask":["Bash(gh pr comment:*)"]}}`,
		"--mcp-config", `{"mcpServers":{"lw":{"type":"http","url":"http://127.0.0.1:1/mcp","headers":{"Authorization":"Bearer t"}}}}`,
	}, cmd.Args)
	assert.Equal(t, "/work", cmd.Dir)
	assert.Contains(t, cmd.Env, "FOO=bar")
	for _, kv := range []string{"CLAUDECODE=1", "NODE_OPTIONS=--inspect", "DEBUG=1"} {
		assert.NotContains(t, cmd.Env, kv)
	}
	assert.Equal(t, len(os.Environ())-2, len(cmd.Env), "three variables are out, and FOO is in")
}

func TestThinkingOffDisablesThinking(t *testing.T) {
	starts := claude.Replay(t, nil, "testdata/text.trace")
	a := claude.New(claude.Thinking(ai.ThinkingOff))
	defer closeAgent(t, a)

	_, _, err := run(context.Background(), a, "Say hello")
	require.NoError(t, err)

	cmds := starts.Cmds()
	require.Len(t, cmds, 1)
	args := strings.Join(cmds[0].Args, " ")
	assert.Contains(t, args, "--thinking disabled")
	assert.NotContains(t, args, "--effort")
}

func TestApprove(t *testing.T) {
	tests := []struct {
		name     string
		file     string
		decision agent.Decision
		err      error
		result   string
		isError  bool
	}{
		{
			name:     "allow",
			file:     "testdata/can_use_tool_allow.trace",
			decision: agent.Decision{Allow: true},
		},
		{
			name: "allow with a changed input",
			file: "testdata/can_use_tool_input.trace",
			decision: agent.Decision{
				Allow: true,
				Input: json.RawMessage(`{"command":"rm -i a.txt"}`),
			},
		},
		{
			name:     "deny",
			file:     "testdata/can_use_tool.trace",
			decision: agent.Decision{Message: "gopi does not answer permission prompts. Set a permission mode that allows the tool."},
			result:   "gopi does not answer permission prompts. Set a permission mode that allows the tool.",
			isError:  true,
		},
		{
			name:    "error",
			file:    "testdata/can_use_tool_error.trace",
			err:     errors.New("the prompt failed"),
			result:  "the prompt failed",
			isError: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claude.Replay(t, nil, tt.file)
			var got []ai.ToolCall
			a := claude.New(claude.Approve(func(ctx context.Context, req agent.ApprovalRequest) (agent.Decision, error) {
				got = append(got, req.Call)
				return tt.decision, tt.err
			}))
			defer closeAgent(t, a)

			events, msgs, err := run(context.Background(), a, "Delete a.txt")
			require.NoError(t, err)
			emittest.CheckEvents(t, events)

			require.Len(t, got, 1)
			assert.Equal(t, "toolu_1", got[0].ID)
			assert.Equal(t, "Bash", got[0].Name)
			assert.JSONEq(t, `{"command":"rm a.txt"}`, string(got[0].Input))

			require.Len(t, msgs, 3)
			assert.Equal(t, tt.result, msgs[1].Text())
			assert.Equal(t, tt.isError, msgs[1].IsError)
			checkLog(t, a)
		})
	}
}

func TestRunEphemeralInput(t *testing.T) {
	claude.Replay(t, nil, "testdata/ephemeral.trace")
	a := claude.New()
	defer closeAgent(t, a)

	s := a.Run(context.Background(),
		session.Text("Say hello"),
		session.Text("Be brief.", session.Ephemeral()),
	)
	var events []agent.Event
	for ev, err := range s.Events() {
		require.NoError(t, err)
		events = append(events, ev)
	}
	_, err := s.Wait()
	require.NoError(t, err)
	emittest.CheckEvents(t, events)

	end, ok := events[5].(agent.EventMessageEnd)
	require.True(t, ok, "the ephemeral message has its events: %T", events[5])
	assert.True(t, end.Ephemeral)

	entries := a.Log().Entries()
	require.Len(t, entries, 2, "the log does not keep the ephemeral entry")
	assert.Equal(t, "Say hello", entries[0].Message.Text())
	checkLog(t, a)
}

func TestCloseSendsSIGTERM(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "term")
	body := `trap 'echo term > ` + marker + `; exit 0' TERM
read line
echo '{"type":"control_response","response":{"subtype":"success","request_id":"req_1","response":{}}}'
while :; do sleep 0.1; done`
	a := claude.New(claude.Binary(fakeCLI(t, body)))
	require.NoError(t, claude.Start(context.Background(), a))

	require.NoError(t, a.Close(), "the CLI exits on SIGTERM before the kill")
	assert.FileExists(t, marker)
}

// fakeCLI writes a shell script that stands in for the CLI and returns
// its path. The script runs body.
func fakeCLI(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude")
	script := "#!/bin/sh\n" + body + "\n"
	require.NoError(t, os.WriteFile(path, []byte(script), 0o700))
	return path
}

func TestRunStderrOnExit(t *testing.T) {
	const reason = "error: option '--permission-mode <mode>' argument 'nope' is invalid."
	tests := []struct {
		name string
		body string
	}{
		{
			name: "the CLI exits at once",
			body: "echo \"" + reason + "\" >&2\nexit 1",
		},
		{
			name: "the CLI exits after the first line",
			body: "read line\necho \"" + reason + "\" >&2\nexit 1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := claude.New(claude.Binary(fakeCLI(t, tt.body)))
			defer a.Close()

			_, _, err := run(context.Background(), a, "Hi")
			require.Error(t, err)
			assert.Contains(t, err.Error(), reason, "the error holds the stderr of the CLI")
			assert.Empty(t, a.Log().Entries(), "the log holds no input that the CLI did not get")
		})
	}
}

func TestRunSendsFiles(t *testing.T) {
	claude.Replay(t, nil, "testdata/files.trace")
	a := claude.New()
	defer closeAgent(t, a)

	input := session.NewEntry(ai.Message{
		Role: ai.RoleUser,
		Content: []ai.Content{
			ai.Text{Text: "What is in these?"},
			ai.File{MediaType: "image/png", Data: []byte("png")},
			ai.File{MediaType: "application/pdf", Data: []byte("pdf")},
			ai.File{MediaType: "audio/wav", Data: []byte("wav")},
		},
	})
	events, msgs, err := collectRun(a.Run(context.Background(), input))
	require.NoError(t, err, "the trace matches the user line with the files and the note")
	emittest.CheckEvents(t, events)
	require.Len(t, msgs, 1)
	assert.Equal(t, "A logo and a report.", msgs[0].Text())
}

func TestRunFileOnly(t *testing.T) {
	claude.Replay(t, nil, "testdata/file_only.trace")
	a := claude.New()
	defer closeAgent(t, a)

	input := session.NewEntry(ai.Message{
		Role:    ai.RoleUser,
		Content: []ai.Content{ai.File{MediaType: "image/png", Data: []byte("png")}},
	})
	_, err := a.Run(context.Background(), input).Wait()
	require.NoError(t, err, "a file without text is input")
}

func TestApproveAlways(t *testing.T) {
	tests := []struct {
		name  string
		file  string
		input json.RawMessage
	}{
		{
			name: "the suggestions of the CLI go back for the session",
			file: "testdata/can_use_tool_always.trace",
		},
		{
			name: "no suggestions send no permission updates",
			file: "testdata/can_use_tool_always_none.trace",
		},
		{
			name:  "the same input in another form keeps the suggestions",
			file:  "testdata/can_use_tool_always.trace",
			input: json.RawMessage(`{ "command": "rm a.txt" }`),
		},
		{
			name:  "a new input sends no permission updates",
			file:  "testdata/can_use_tool_always_input.trace",
			input: json.RawMessage(`{"command":"rm -i a.txt"}`),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claude.Replay(t, nil, tt.file)
			a := claude.New(claude.Approve(func(ctx context.Context, _ agent.ApprovalRequest) (agent.Decision, error) {
				return agent.Decision{
					Allow:  true,
					Always: true,
					Input:  tt.input,
				}, nil
			}))
			defer closeAgent(t, a)

			_, _, err := run(context.Background(), a, "Delete a.txt")
			require.NoError(t, err, "the trace matches the answer to the prompt")
		})
	}
}

func TestApproveRequest(t *testing.T) {
	claude.Replay(t, nil, "testdata/can_use_tool_no_always.trace")
	var got agent.ApprovalRequest
	a := claude.New(claude.Approve(func(_ context.Context, req agent.ApprovalRequest) (agent.Decision, error) {
		got = req
		return agent.Decision{
			Allow:  true,
			Always: true,
		}, nil
	}))
	defer closeAgent(t, a)

	_, _, err := run(context.Background(), a, "Delete a.txt")
	require.NoError(t, err, "an Always decision on a prompt with no allow always sends no permission updates")
	assert.Equal(t, "rm deletes files", got.Reason, "the request has the reason with no escapes")
	assert.True(t, got.NoAlways)
}

func TestRunNotLoggedIn(t *testing.T) {
	claude.Replay(t, nil, "testdata/auth.trace")
	a := claude.New()
	defer closeAgent(t, a)

	events, _, err := run(context.Background(), a, "Hi")
	require.ErrorIs(t, err, claude.ErrAuth)
	assert.ErrorContains(t, err, "Please run /login")
	emittest.CheckEvents(t, events)
}

// collectRun reads every event of s, then its result.
func collectRun(s *agent.Stream) ([]agent.Event, []ai.Message, error) {
	var events []agent.Event
	for ev, err := range s.Events() {
		if err != nil {
			break
		}
		events = append(events, ev)
	}
	msgs, err := s.Wait()
	return events, msgs, err
}

func TestSubagentOpenAtTheResultEndsInterrupted(t *testing.T) {
	ctx := context.Background()
	claude.Replay(t, nil, "testdata/subagent_open_at_result.trace")
	store := session.NewMemoryStore()
	a, err := claude.Durable(ctx, "s1", claude.Store(store))
	require.NoError(t, err)
	defer closeAgent(t, a)

	events, _, err := run(ctx, a, "Use the Agent tool once.")
	require.NoError(t, err)
	emittest.CheckEvents(t, events)

	var (
		end   agent.EventSubagentEnd
		inner []agent.Event
	)
	for _, e := range events {
		switch e := e.(type) {
		case agent.EventSubagentEnd:
			end = e
		case agent.EventSubagent:
			inner = append(inner, e.Event)
		}
	}
	assert.Equal(t, agent.SubagentInterrupted, end.Status, "the run ends the child that is open")
	emittest.CheckEvents(t, inner)
	checkLog(t, a)
}

func TestSubagentWhoseResultComesFirstEndsWithItsText(t *testing.T) {
	ctx := context.Background()
	claude.Replay(t, nil, "testdata/subagent_result_first.trace")
	a, err := claude.Durable(ctx, "s1", claude.Store(session.NewMemoryStore()))
	require.NoError(t, err)
	defer closeAgent(t, a)

	events, _, err := run(ctx, a, "Use the Agent tool once.")
	require.NoError(t, err)
	emittest.CheckEvents(t, events)

	var ends []agent.EventSubagentEnd
	for _, e := range events {
		if end, ok := e.(agent.EventSubagentEnd); ok {
			ends = append(ends, end)
		}
	}
	require.Len(t, ends, 1)
	assert.Equal(t, agent.SubagentCompleted, ends[0].Status)
	assert.Equal(t, "banana", ends[0].Text, "the child ends at its task_notification, with the summary")
}

// childFailStore is a store whose appends to a child session fail.
type childFailStore struct {
	*session.MemoryStore
}

func (s childFailStore) Append(ctx context.Context, id string, entries ...session.Entry) error {
	if strings.Contains(id, ".") {
		return errors.New("disk full")
	}
	return s.MemoryStore.Append(ctx, id, entries...)
}

func TestSubagentWithAFailedLogStillEnds(t *testing.T) {
	ctx := context.Background()
	claude.Replay(t, nil, "testdata/subagent_open_at_result.trace")
	a, err := claude.Durable(ctx, "s1", claude.Store(childFailStore{session.NewMemoryStore()}))
	require.NoError(t, err)
	defer closeAgent(t, a)

	events, _, err := run(ctx, a, "Use the Agent tool once.")
	require.ErrorContains(t, err, "disk full", "the run reports the failure of the child log")
	emittest.CheckEvents(t, events)

	var ends int
	for _, e := range events {
		if _, ok := e.(agent.EventSubagentEnd); ok {
			ends++
		}
	}
	assert.Equal(t, 1, ends, "the child that started also ends")
}

func TestInputWaitsForTheTurnThatTheCLIStarted(t *testing.T) {
	ctx := context.Background()
	claude.Replay(t, nil, "testdata/background_wake_input.trace")
	allow := func(ctx context.Context, req agent.ApprovalRequest) (agent.Decision, error) {
		return agent.Decision{Allow: true}, nil
	}
	woke := newWakeSink()
	a := claude.New(
		claude.Approve(allow),
		claude.Wake(woke.signal),
	)
	defer closeAgent(t, a)

	_, _, err := run(ctx, a, "Start a helper in the background.")
	require.NoError(t, err)
	<-woke

	// The turn of the CLI waits for the answer to its request, so it is
	// still open when the run starts. The trace fails the run if the
	// input goes before the result of that turn.
	events, msgs, err := run(ctx, a, "What next?")
	require.NoError(t, err)
	emittest.CheckEvents(t, events)

	var texts []string
	for _, m := range msgs {
		texts = append(texts, m.Text())
	}
	assert.Contains(t, texts, "The helper found banana.", "the run keeps the turn that the CLI started")
	assert.Equal(t, "Nothing.", texts[len(texts)-1])
	checkLog(t, a)
}

func TestContinueWithNoTurnEndsAtOnce(t *testing.T) {
	claude.Replay(t, nil, "testdata/text.trace")
	a := claude.New()
	defer closeAgent(t, a)
	_, _, err := run(context.Background(), a, "Say hello")
	require.NoError(t, err)
	before := len(a.Log().Entries())

	_, msgs, err := collectRun(a.Continue(context.Background()))
	require.NoError(t, err)
	assert.Empty(t, msgs)
	assert.Len(t, a.Log().Entries(), before, "a run with no turn stores nothing")
}

// wakeSink is a channel that gets one value for each wake of the agent
// that no test read yet.
type wakeSink chan struct{}

func newWakeSink() wakeSink { return make(wakeSink, 1) }

func (w wakeSink) signal() {
	select {
	case w <- struct{}{}:
	default:
	}
}
