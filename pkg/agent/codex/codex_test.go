package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/internal/stdiorr"
	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/internal/cliproc"
	"github.com/sonnes/gopi/pkg/agent/internal/emittest"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
)

// workDir is the working directory in the hand-written traces.
const workDir = "/work"

// useTraces replaces the process hook for the test. Each process start
// replays the next trace in files, in order. A start after the last
// file fails the test.
func useTraces(t *testing.T, files []string, scrubs ...stdiorr.Scrub) {
	t.Helper()
	saved := start
	t.Cleanup(func() { start = saved })
	start = func(cmd *exec.Cmd) (cliproc.Pipes, error) {
		if len(files) == 0 {
			t.Errorf("codex: an unexpected process start")
			return cliproc.Pipes{}, os.ErrNotExist
		}
		file := files[0]
		files = files[1:]
		p, err := stdiorr.Open(t, filepath.Join("testdata", file), cmd, scrubs...)
		if err != nil {
			return cliproc.Pipes{}, err
		}
		return cliproc.Pipes{
			Stdin:  p.Stdin,
			Stdout: p.Stdout,
			Wait:   p.Wait,
			Kill:   p.Kill,
		}, nil
	}
}

// collect reads every event of s, then its result.
func collect(s *agent.Stream) ([]agent.Event, []ai.Message, error) {
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

// checkLog fails t if an assistant message in the log of a has a tool
// call with no tool result, or if the state of a does not hold the
// messages of the log.
func checkLog(t *testing.T, a *Agent) {
	t.Helper()
	log := a.Log()
	assert.Equal(t, log.Messages(), a.State().Messages(), "the state has the messages of the log")
	assert.False(t, a.State().Running())
	results := map[string]bool{}
	for _, en := range log.Entries() {
		if en.Message.Role == ai.RoleToolResult {
			results[en.Message.ToolCallID] = true
		}
	}
	for _, en := range log.Entries() {
		for _, tc := range en.Message.ToolCalls() {
			assert.True(t, results[tc.ID], "tool call %q has a result in the log", tc.ID)
		}
	}
}

// lastEnd returns the agent_end of a run.
func lastEnd(t *testing.T, events []agent.Event) agent.EventAgentEnd {
	t.Helper()
	require.NotEmpty(t, events)
	end, ok := events[len(events)-1].(agent.EventAgentEnd)
	require.True(t, ok, "the last event is agent_end, not %T", events[len(events)-1])
	return end
}

// deltas returns the deltas of the message_update events of type typ.
func deltas(events []agent.Event, typ ai.EventType) []string {
	var out []string
	for _, ev := range events {
		up, ok := ev.(agent.EventMessageUpdate)
		if ok && up.AssistantEvent.Type == typ {
			out = append(out, up.AssistantEvent.Delta)
		}
	}
	return out
}

// newAgent returns an agent over an unsaved log in workDir, and closes
// it at the end of the test.
func newAgent(t *testing.T, opts ...Option) *Agent {
	t.Helper()
	opts = append([]Option{Dir(workDir)}, opts...)
	a := New(opts...)
	t.Cleanup(func() { a.Close() })
	return a
}

func TestRunText(t *testing.T) {
	useTraces(t, []string{"text.trace"})
	a := newAgent(t,
		Model("gpt-5-codex"),
		System("Be brief."),
		Sandbox(SandboxReadOnly),
		Thinking(ai.ThinkingHigh),
		Name("codex"),
	)

	events, msgs, err := collect(a.Run(context.Background(), session.Text("Say hello.")))
	require.NoError(t, err)
	emittest.CheckEvents(t, events)

	require.Len(t, msgs, 1)
	assert.Equal(t, "Hello", msgs[0].Text())
	assert.Equal(t, ai.StopReasonStop, msgs[0].StopReason)
	assert.Equal(t, "gpt-5-codex", msgs[0].Model)

	end := lastEnd(t, events)
	assert.Equal(t, ai.Usage{Input: 60, CacheRead: 40, Output: 20}, end.Usage)
	assert.Equal(t, []string{"Hel", "lo"}, deltas(events, ai.EventTextDelta))
	checkLog(t, a)

	entries := a.Log().Entries()
	require.Len(t, entries, 3)
	assert.Equal(t, ai.RoleSystem, entries[0].Message.Role, "the log keeps the base")
	assert.Equal(t, ai.RoleUser, entries[1].Message.Role)
	assert.Equal(t, "codex", entries[2].Author)

	thread, err := conversation.Get(a.Log())
	require.NoError(t, err)
	assert.Equal(t, "th1", thread)
}

func TestRunThinking(t *testing.T) {
	useTraces(t, []string{"thinking.trace"})
	a := newAgent(t)

	events, msgs, err := collect(a.Run(context.Background(), session.Text("Think, then answer.")))
	require.NoError(t, err)
	emittest.CheckEvents(t, events)

	require.Len(t, msgs, 1)
	assert.Equal(t, []ai.Content{
		ai.ThinkingBlock{Thinking: "Plan it."},
		ai.Text{Text: "Done."},
	}, msgs[0].Content, "an empty reasoning item adds no block")
}

func TestRunTool(t *testing.T) {
	tests := []struct {
		name      string
		file      string
		wantText  string
		wantError bool
	}{
		{
			name:     "success",
			file:     "tool.trace",
			wantText: "hi\n",
		},
		{
			name:      "nonzero exit",
			file:      "tool_error.trace",
			wantText:  "sh: nope: not found\n",
			wantError: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			useTraces(t, []string{tt.file})
			a := newAgent(t)

			events, msgs, err := collect(a.Run(context.Background(), session.Text("Run echo hi.")))
			require.NoError(t, err)
			emittest.CheckEvents(t, events)
			checkLog(t, a)

			require.Len(t, msgs, 3)
			assert.Equal(t, ai.StopReasonToolUse, msgs[0].StopReason)
			assert.Equal(t, "Running it.", msgs[0].Text())
			calls := msgs[0].ToolCalls()
			require.Len(t, calls, 1)
			assert.Equal(t, "c1", calls[0].ID)
			assert.Equal(t, "bash", calls[0].Name)
			assert.JSONEq(t, `{"command":"echo hi"}`, string(calls[0].Input))

			assert.Equal(t, ai.RoleToolResult, msgs[1].Role)
			assert.Equal(t, "c1", msgs[1].ToolCallID)
			assert.Equal(t, tt.wantError, msgs[1].IsError)
			assert.Equal(t, []ai.Content{ai.Text{Text: tt.wantText}}, msgs[1].Content)

			assert.Equal(t, ai.StopReasonStop, msgs[2].StopReason)

			assert.Equal(t, []string{
				"agent_start",
				"turn_start",
				"message_start", // input
				"message_end",
				"message_start",
				"message_update", // text start
				"message_update", // text delta
				"message_update", // text end
				"message_update", // tool call
				"message_end",
				"tool_execution_start",
				"tool_execution_end",
				"message_start",
				"message_end",
				"turn_end",
				"turn_start",
				"message_start",
				"message_update",
				"message_update",
				"message_update",
				"message_end",
				"turn_end",
				"agent_end",
			}, emittest.Types(events))

			end := lastEnd(t, events)
			assert.Equal(t, ai.Usage{Input: 90, CacheRead: 140, Output: 25}, end.Usage,
				"the usage sums the last request of each update")
		})
	}
}

func TestRunEphemeralInput(t *testing.T) {
	useTraces(t, []string{"ephemeral.trace"})
	a := newAgent(t,
		Model("gpt-5-codex"),
		System("Be brief."),
		Sandbox(SandboxReadOnly),
		Thinking(ai.ThinkingHigh),
	)

	events, _, err := collect(a.Run(context.Background(),
		session.Text("Say hello."),
		session.Text("Be brief.", session.Ephemeral()),
	))
	require.NoError(t, err)
	emittest.CheckEvents(t, events)

	end, ok := events[7].(agent.EventMessageEnd)
	require.True(t, ok, "the ephemeral message has its events: %T", events[7])
	assert.True(t, end.Ephemeral)

	entries := a.Log().Entries()
	require.Len(t, entries, 3, "the log keeps the base, and not the ephemeral entry")
	assert.Equal(t, "Say hello.", entries[1].Message.Text())
	checkLog(t, a)
}

func TestRunTwice(t *testing.T) {
	useTraces(t, []string{"two_runs.trace"})
	a := newAgent(t)

	for _, input := range []string{"One.", "Two."} {
		events, _, err := collect(a.Run(context.Background(), session.Text(input)))
		require.NoError(t, err)
		emittest.CheckEvents(t, events)
	}

	var texts []string
	for _, m := range a.Log().Messages() {
		texts = append(texts, m.Text())
	}
	assert.Equal(t, []string{"One.", "First.", "Two.", "Second."}, texts)
}

func TestDurableResume(t *testing.T) {
	useTraces(t, []string{"resume_first.trace", "resume_second.trace"})
	ctx := context.Background()
	store := session.NewMemoryStore()

	first, err := Durable(ctx, "s1", Store(store), Dir(workDir))
	require.NoError(t, err)
	events, _, err := collect(first.Run(ctx, session.Text("Remember 7.")))
	require.NoError(t, err)
	emittest.CheckEvents(t, events)
	require.NoError(t, first.Close())

	second, err := Durable(ctx, "s1", Store(store), Dir(workDir))
	require.NoError(t, err)
	t.Cleanup(func() { second.Close() })
	assert.Len(t, second.State().Messages(), 2, "the state starts with the stored messages")
	events, msgs, err := collect(second.Run(ctx, session.Text("What number?")))
	require.NoError(t, err)
	emittest.CheckEvents(t, events)

	require.Len(t, msgs, 1)
	assert.Equal(t, "7.", msgs[0].Text())
	assert.Len(t, second.Log().Entries(), 4, "the second agent reads the stored session")
}

func TestRunCancel(t *testing.T) {
	useTraces(t, []string{"cancel.trace"})
	a := newAgent(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := a.Run(ctx, session.Text("Count to 100."))
	var events []agent.Event
	var runErr error
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
	require.ErrorIs(t, runErr, context.Canceled)
	emittest.CheckEvents(t, events)
	assert.Equal(t, context.Canceled.Error(), lastEnd(t, events).Error)

	entries := a.Log().Entries()
	require.Len(t, entries, 1, "the partial message is not stored")
}

func TestRunFailed(t *testing.T) {
	useTraces(t, []string{"failed.trace"})
	a := newAgent(t)

	events, _, err := collect(a.Run(context.Background(), session.Text("Hi.")))
	require.ErrorContains(t, err, "usage limit reached")
	emittest.CheckEvents(t, events)
}

func TestServerRequests(t *testing.T) {
	// The ">" line of each trace holds the answer of the client.
	tests := []struct {
		name     string
		file     string
		input    string
		approval ApprovalPolicy
		wantText string
	}{
		{
			name:     "approval is declined",
			file:     "approval.trace",
			input:    "Remove /tmp/x.",
			approval: ApprovalUntrusted,
			wantText: "The command was declined.",
		},
		{
			name:     "unknown request gets an error",
			file:     "unknown_request.trace",
			input:    "Ask me.",
			approval: ApprovalNever,
			wantText: "No answer.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			useTraces(t, []string{tt.file})
			a := newAgent(t, Policy(tt.approval))

			events, msgs, err := collect(a.Run(context.Background(), session.Text(tt.input)))
			require.NoError(t, err)
			emittest.CheckEvents(t, events)
			checkLog(t, a)

			require.NotEmpty(t, msgs)
			assert.Equal(t, tt.wantText, msgs[len(msgs)-1].Text())
		})
	}
}

func TestDeclinedCommandIsError(t *testing.T) {
	useTraces(t, []string{"approval.trace"})
	a := newAgent(t, Policy(ApprovalUntrusted))

	_, msgs, err := collect(a.Run(context.Background(), session.Text("Remove /tmp/x.")))
	require.NoError(t, err)
	require.Len(t, msgs, 3)
	assert.True(t, msgs[1].IsError)
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
			useTraces(t, nil)
			a := newAgent(t)

			_, err := a.Run(context.Background(), tt.entries...).Wait()
			assert.ErrorContains(t, err, "a run needs an entry with text")
			assert.Empty(t, a.Log().Entries())
		})
	}
}

func TestRunFailedKeepsCompletedItems(t *testing.T) {
	useTraces(t, []string{"failed_message.trace"})
	a := newAgent(t)

	events, msgs, err := collect(a.Run(context.Background(), session.Text("Fix the test.")))
	require.ErrorContains(t, err, "usage limit reached")
	emittest.CheckEvents(t, events)
	checkLog(t, a)

	require.Len(t, msgs, 1, "the completed item is stored")
	assert.Equal(t, []ai.Content{ai.Text{Text: "The fault is in parse.go."}}, msgs[0].Content,
		"the item in flight is not stored")
	assert.Equal(t, ai.StopReasonError, msgs[0].StopReason)
}

func TestRunCancelKeepsCompletedItems(t *testing.T) {
	useTraces(t, []string{"cancel_completed.trace"})
	a := newAgent(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := a.Run(ctx, session.Text("Count to 100."))
	var events []agent.Event
	for ev, err := range s.Events() {
		if err != nil {
			break
		}
		events = append(events, ev)
		if _, ok := ev.(agent.EventMessageUpdate); ok {
			cancel()
		}
	}
	msgs, err := s.Wait()
	require.ErrorIs(t, err, context.Canceled)
	emittest.CheckEvents(t, events)
	checkLog(t, a)

	require.Len(t, msgs, 1, "the item that completed after the cancel is stored")
	assert.Equal(t, "1, 2, 3", msgs[0].Text())
	assert.Equal(t, ai.StopReasonAborted, msgs[0].StopReason)
}

func TestRunReasoningWithoutFinalText(t *testing.T) {
	useTraces(t, []string{"reasoning_empty.trace"})
	a := newAgent(t)

	events, msgs, err := collect(a.Run(context.Background(), session.Text("Think.")))
	require.NoError(t, err)
	emittest.CheckEvents(t, events)

	require.Len(t, msgs, 1)
	assert.Equal(t, ai.ThinkingBlock{Thinking: "S1"}, msgs[0].Content[0], "the streamed text stays")
}

func TestRunCountsEachRequestOnce(t *testing.T) {
	useTraces(t, []string{"usage_repeat.trace"})
	a := newAgent(t)

	events, _, err := collect(a.Run(context.Background(), session.Text("Hi.")))
	require.NoError(t, err)

	want := ai.Usage{
		Input:     60,
		Output:    20,
		CacheRead: 40,
	}
	assert.Equal(t, want, lastEnd(t, events).Usage, "a repeated update adds nothing")
}

func TestRunReadError(t *testing.T) {
	// The line is 1 byte longer than the limit of a line.
	body := "read line\nprintf '%67108865s\\n' ''"
	a := New(Binary(fakeCLI(t, body)))
	defer a.Close()

	_, _, err := collect(a.Run(context.Background(), session.Text("Hi.")))
	require.ErrorIs(t, err, errExited)
	assert.ErrorContains(t, err, "token too long", "the error holds the read error")
}

func TestRunLostThread(t *testing.T) {
	ctx := context.Background()
	useTraces(t, []string{"lost_thread.trace", "lost_thread_new.trace"})
	a := newAgent(t)
	require.NoError(t, conversation.Save(ctx, a.log, "th0"))

	events, _, err := collect(a.Run(ctx, session.Text("Hi.")))
	require.ErrorIs(t, err, ErrNoConversation)
	emittest.CheckEvents(t, events)
	id, err := conversation.Get(a.log)
	require.NoError(t, err)
	assert.Empty(t, id, "the agent forgets the lost thread")

	_, msgs, err := collect(a.Run(ctx, session.Text("Hi.")))
	require.NoError(t, err, "the next run starts a new thread")
	require.Len(t, msgs, 1)
	assert.Equal(t, "Hello", msgs[0].Text())
}

func TestRunConcurrent(t *testing.T) {
	useTraces(t, []string{"concurrent.trace"})
	entered := make(chan struct{})
	release := make(chan struct{})
	replay := start
	start = func(cmd *exec.Cmd) (cliproc.Pipes, error) {
		close(entered)
		<-release
		return replay(cmd)
	}
	a := newAgent(t)

	first := a.Run(context.Background(), session.Text("Hi."))
	<-entered
	_, _, err := collect(a.Run(context.Background(), session.Text("Again.")))
	require.ErrorContains(t, err, "a run is active")
	close(release)

	events, msgs, err := collect(first)
	require.NoError(t, err)
	emittest.CheckEvents(t, events)
	require.Len(t, msgs, 1)
	assert.Equal(t, "Hello", msgs[0].Text())
}

func TestEffort(t *testing.T) {
	tests := []struct {
		level ai.ThinkingLevel
		want  string
	}{
		{level: "", want: ""},
		{level: ai.ThinkingOff, want: ""},
		{level: ai.ThinkingLow, want: "low"},
		{level: ai.ThinkingMedium, want: "medium"},
		{level: ai.ThinkingHigh, want: "high"},
		{level: ai.ThinkingXHigh, want: "xhigh"},
		{level: ai.ThinkingMax, want: "xhigh"},
	}
	for _, tt := range tests {
		t.Run(string(tt.level), func(t *testing.T) {
			assert.Equal(t, tt.want, effort(tt.level))
		})
	}
}

// replaceScrub returns a scrub that replaces old with repl.
func replaceScrub(old, repl string) stdiorr.Scrub {
	return func(line []byte) []byte {
		return bytes.ReplaceAll(line, []byte(old), []byte(repl))
	}
}

// machineIDs matches the fields that name the machine of a recording.
var machineIDs = regexp.MustCompile(`"(serverName|installationId)":"[^"]*"`)

// machineScrub replaces the fields that name the machine.
func machineScrub(line []byte) []byte {
	return machineIDs.ReplaceAll(line, []byte(`"$1":"scrubbed"`))
}

// jsonString returns s as the body of a JSON string, so that a scrub
// matches the escaped form in a line.
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}

// TestRecorded replays a trace of the real CLI. To record it again:
//
//	make record-cli AGENT=codex RUN=TestRecorded
func TestRecorded(t *testing.T) {
	dir := t.TempDir()
	real, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	useTraces(t, []string{"TestRecorded.trace"},
		replaceScrub(jsonString(real), workDir),
		replaceScrub(jsonString(dir), workDir),
		replaceScrub(jsonString(home), "/home/me"),
		machineScrub,
	)
	a := New(
		Dir(dir),
		Sandbox(SandboxReadOnly),
		Policy(ApprovalNever),
		Thinking(ai.ThinkingLow),
	)
	t.Cleanup(func() { a.Close() })

	prompt := "Run `echo hi` in the shell, then reply with its output only."
	events, msgs, err := collect(a.Run(context.Background(), session.Text(prompt)))
	require.NoError(t, err)
	emittest.CheckEvents(t, events)
	checkLog(t, a)

	var ran bool
	for _, m := range msgs {
		for _, tc := range m.ToolCalls() {
			ran = ran || tc.Name == "bash"
		}
	}
	assert.True(t, ran, "the agent runs a shell command")
	require.NotEmpty(t, msgs)
	last := msgs[len(msgs)-1]
	assert.Contains(t, last.Text(), "hi")
	assert.NotEmpty(t, last.Model)
}

func TestRunIgnoresOtherTurns(t *testing.T) {
	useTraces(t, []string{"stale.trace"})
	a := newAgent(t)

	events, msgs, err := collect(a.Run(context.Background(), session.Text("Hi.")))
	require.NoError(t, err)
	emittest.CheckEvents(t, events)

	require.Len(t, msgs, 1)
	assert.Equal(t, "Hello", msgs[0].Text())
	assert.Zero(t, lastEnd(t, events).Usage, "the usage of another turn is not counted")
}

func TestRunParallelTools(t *testing.T) {
	useTraces(t, []string{"parallel.trace"})
	a := newAgent(t)

	events, msgs, err := collect(a.Run(context.Background(), session.Text("Run both.")))
	require.NoError(t, err)
	emittest.CheckEvents(t, events)
	checkLog(t, a)

	// The second command waits until the first one completes.
	require.Len(t, msgs, 5)
	assert.Equal(t, "Running both.", msgs[0].Text())
	assert.Equal(t, []string{"c1"}, callIDs(msgs[0]))
	assert.Equal(t, "c1", msgs[1].ToolCallID)
	assert.Equal(t, []ai.Content{ai.Text{Text: "a\n"}}, msgs[1].Content)
	assert.Equal(t, []string{"c2"}, callIDs(msgs[2]))
	assert.Equal(t, "c2", msgs[3].ToolCallID)
	assert.Equal(t, []ai.Content{ai.Text{Text: "b\n"}}, msgs[3].Content)
	assert.Equal(t, "Done.", msgs[4].Text())
}

func TestRunTextDuringTool(t *testing.T) {
	useTraces(t, []string{"text_during_tool.trace"})
	a := newAgent(t)

	events, msgs, err := collect(a.Run(context.Background(), session.Text("Run it and talk.")))
	require.NoError(t, err)
	emittest.CheckEvents(t, events)
	checkLog(t, a)

	// The text waits until the command completes.
	require.Len(t, msgs, 3)
	assert.Equal(t, []string{"c1"}, callIDs(msgs[0]))
	assert.Equal(t, "c1", msgs[1].ToolCallID)
	assert.Equal(t, "Working.", msgs[2].Text())
	assert.Equal(t, ai.StopReasonStop, msgs[2].StopReason)
}

func TestRunCancelDuringTool(t *testing.T) {
	useTraces(t, []string{"cancel_tool.trace"})
	a := newAgent(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := a.Run(ctx, session.Text("Run sleep."))
	var events []agent.Event
	var runErr error
	for ev, err := range s.Events() {
		if err != nil {
			runErr = err
			break
		}
		events = append(events, ev)
		if _, ok := ev.(agent.EventToolExecutionStart); ok {
			cancel()
		}
	}
	require.ErrorIs(t, runErr, context.Canceled)
	emittest.CheckEvents(t, events)

	checkLog(t, a)

	entries := a.Log().Entries()
	require.Len(t, entries, 3, "the text, the call, and the result are stored")
	assert.Equal(t, "Running it.", entries[1].Message.Text())
	assert.Equal(t, []string{"c1"}, callIDs(entries[1].Message))
	result := entries[2].Message
	assert.Equal(t, "c1", result.ToolCallID)
	assert.True(t, result.IsError)
	assert.Equal(t, []ai.Content{ai.Text{Text: "interrupted\n"}}, result.Content,
		"the result is the one that the CLI sends after the cancel")
}

func TestRunToolUnfinished(t *testing.T) {
	useTraces(t, []string{"tool_unfinished.trace"})
	a := newAgent(t)

	events, msgs, err := collect(a.Run(context.Background(), session.Text("Run it.")))
	require.NoError(t, err)
	emittest.CheckEvents(t, events)
	checkLog(t, a)

	require.Len(t, msgs, 3)
	assert.Equal(t, []string{"c1"}, callIDs(msgs[0]))
	assert.True(t, msgs[1].IsError)
	assert.Equal(t, []ai.Content{ai.Text{Text: "the turn ended before the tool completed"}}, msgs[1].Content)
	assert.Equal(t, "Final.", msgs[2].Text(), "the queued answer is not lost")
}

func TestRunDuplicateItem(t *testing.T) {
	useTraces(t, []string{"duplicate.trace"})
	a := newAgent(t)

	events, msgs, err := collect(a.Run(context.Background(), session.Text("Run it.")))
	require.NoError(t, err)
	emittest.CheckEvents(t, events)
	checkLog(t, a)

	require.Len(t, msgs, 3)
	assert.Equal(t, []string{"c1"}, callIDs(msgs[0]))
	assert.Equal(t, "c1", msgs[1].ToolCallID)
	assert.Equal(t, "Done.", msgs[2].Text())
}

func TestRunReasoningParts(t *testing.T) {
	useTraces(t, []string{"reasoning_parts.trace"})
	a := newAgent(t)

	events, msgs, err := collect(a.Run(context.Background(), session.Text("Think.")))
	require.NoError(t, err)
	emittest.CheckEvents(t, events)

	var (
		streamed string
		last     ai.Content
	)
	for _, ev := range events {
		up, ok := ev.(agent.EventMessageUpdate)
		if ok && up.AssistantEvent.Type == ai.EventThinkDelta {
			streamed += up.AssistantEvent.Delta
			last = up.Message.Content[0]
		}
	}
	assert.Equal(t, "S1\n\nC1\n\nC2", streamed, "each new part starts with the separator")
	require.Len(t, msgs, 1)
	assert.Equal(t, msgs[0].Content[0], last, "the last streamed snapshot is the stored block")
}

// callIDs returns the IDs of the tool calls of m.
func callIDs(m ai.Message) []string {
	var ids []string
	for _, tc := range m.ToolCalls() {
		ids = append(ids, tc.ID)
	}
	return ids
}

func TestRunConnectFails(t *testing.T) {
	tests := []struct {
		name  string
		start func(t *testing.T)
	}{
		{
			name: "missing binary",
			start: func(t *testing.T) {
				saved := start
				t.Cleanup(func() { start = saved })
				start = func(*exec.Cmd) (cliproc.Pipes, error) {
					return cliproc.Pipes{}, exec.ErrNotFound
				}
			},
		},
		{
			name: "thread/start fails",
			start: func(t *testing.T) {
				useTraces(t, []string{"handshake_error.trace"})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.start(t)
			a := newAgent(t)

			events, _, err := collect(a.Run(context.Background(), session.Text("Hi.")))
			require.Error(t, err)
			assert.Equal(t, []string{"agent_start", "agent_end"}, emittest.Types(events))
			assert.Empty(t, a.Log().Entries(), "the log holds no input that the CLI did not get")
		})
	}
}

func TestRunRestartsAfterError(t *testing.T) {
	useTraces(t, []string{"bad_item.trace", "bad_item_restart.trace"})
	a := newAgent(t)

	events, _, err := collect(a.Run(context.Background(), session.Text("One.")))
	require.Error(t, err)
	emittest.CheckEvents(t, events)

	events, msgs, err := collect(a.Run(context.Background(), session.Text("Two.")))
	require.NoError(t, err)
	emittest.CheckEvents(t, events)
	require.Len(t, msgs, 1)
	assert.Equal(t, "Second.", msgs[0].Text())
}

func TestRunSummaryParts(t *testing.T) {
	useTraces(t, []string{"summary_parts.trace"})
	a := newAgent(t)

	events, msgs, err := collect(a.Run(context.Background(), session.Text("Think.")))
	require.NoError(t, err)
	emittest.CheckEvents(t, events)

	want := "One.\n\nTwo."
	var partial string
	for _, ev := range events {
		up, ok := ev.(agent.EventMessageUpdate)
		if !ok || up.AssistantEvent.Type != ai.EventThinkDelta {
			continue
		}
		b, ok := up.Message.Content[0].(ai.ThinkingBlock)
		require.True(t, ok)
		partial = b.Thinking
	}
	assert.Equal(t, want, partial, "the streamed text matches the final text")
	require.Len(t, msgs, 1)
	assert.Equal(t, ai.ThinkingBlock{Thinking: want}, msgs[0].Content[0])
}

func TestRunUnmappedItem(t *testing.T) {
	useTraces(t, []string{"unmapped.trace"})
	a := newAgent(t)

	events, _, err := collect(a.Run(context.Background(), session.Text("Plan, then answer.")))
	require.ErrorContains(t, err, "stopped")
	emittest.CheckEvents(t, events)

	streamed := strings.Join(deltas(events, ai.EventTextDelta), "")
	assert.Equal(t, "Hello", streamed, "an unmapped item does not hold back the text")
}

func TestRunBadTurnCompleted(t *testing.T) {
	useTraces(t, []string{"bad_completed.trace"})
	a := newAgent(t)

	events, _, err := collect(a.Run(context.Background(), session.Text("Hi.")))
	require.ErrorContains(t, err, "turn/completed")
	emittest.CheckEvents(t, events)
}

// fakeCLI writes a shell script that stands in for the CLI and returns
// its path. The script runs body.
func fakeCLI(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "codex")
	script := "#!/bin/sh\n" + body + "\n"
	require.NoError(t, os.WriteFile(path, []byte(script), 0o700))
	return path
}

func TestRunStderrOnExit(t *testing.T) {
	const reason = "ERROR codex_app_server: not logged in"
	tests := []struct {
		name string
		body string
	}{
		{
			name: "the app server exits at once",
			body: "printf '\\033[31mERROR\\033[0m codex_app_server: not logged in\\n' >&2\nexit 1",
		},
		{
			name: "the app server exits after the first line",
			body: "read line\nprintf '\\033[31mERROR\\033[0m codex_app_server: not logged in\\n' >&2\nexit 1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := New(Binary(fakeCLI(t, tt.body)))
			defer a.Close()

			_, _, err := collect(a.Run(context.Background(), session.Text("Hi.")))
			require.Error(t, err)
			assert.ErrorIs(t, err, errExited)
			assert.Contains(t, err.Error(), reason, "the error holds stderr without color codes")
			assert.Empty(t, a.Log().Entries(), "the log holds no input that the CLI did not get")
		})
	}
}

func TestRunSendsFiles(t *testing.T) {
	useTraces(t, []string{"files.trace"})
	a := newAgent(t)

	input := session.NewEntry(ai.Message{
		Role: ai.RoleUser,
		Content: []ai.Content{
			ai.Text{Text: "What is in these?"},
			ai.File{MediaType: "image/png", Data: []byte("png")},
			ai.File{MediaType: "application/pdf", Data: []byte("pdf")},
		},
	})
	events, msgs, err := collect(a.Run(context.Background(), input))
	require.NoError(t, err, "the trace matches the input with the image and the note")
	emittest.CheckEvents(t, events)
	require.Len(t, msgs, 1)
	assert.Equal(t, "A logo.", msgs[0].Text())
}

func TestApprove(t *testing.T) {
	command := ai.ToolCall{
		ID:    "c1",
		Name:  "bash",
		Input: json.RawMessage(`{"command":"rm -rf /tmp/x"}`),
	}
	tests := []struct {
		name     string
		file     string
		decision agent.Decision
		err      error
		want     ai.ToolCall
		// reason is the reason of the request of the call.
		reason string
	}{
		{
			name:     "allow a command",
			file:     "approval_accept.trace",
			decision: agent.Decision{Allow: true},
			want:     command,
		},
		{
			name: "allow a command for the session",
			file: "approval_always.trace",
			decision: agent.Decision{
				Allow:  true,
				Always: true,
			},
			want: command,
		},
		{
			name: "a changed input declines",
			file: "approval.trace",
			decision: agent.Decision{
				Allow: true,
				Input: json.RawMessage(`{"command":"rm -ri /tmp/x"}`),
			},
			want: command,
		},
		{
			name: "an error declines",
			file: "approval.trace",
			err:  errors.New("no user"),
			want: command,
		},
		{
			name:     "deny a file change",
			file:     "approval_file.trace",
			decision: agent.Decision{},
			want: ai.ToolCall{
				ID:    "c1",
				Name:  "apply_patch",
				Input: json.RawMessage(`{"changes":[{"path":"/tmp/x","kind":{"type":"delete"}}]}`),
			},
			reason: "write outside the workspace",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			useTraces(t, []string{tt.file})
			var (
				got     []ai.ToolCall
				reasons []string
			)
			a := newAgent(t,
				Policy(ApprovalUntrusted),
				Approve(func(_ context.Context, req agent.ApprovalRequest) (agent.Decision, error) {
					got = append(got, req.Call)
					reasons = append(reasons, req.Reason)
					return tt.decision, tt.err
				}),
			)

			events, _, err := collect(a.Run(context.Background(), session.Text("Remove /tmp/x.")))
			require.NoError(t, err, "the trace matches the decision")
			emittest.CheckEvents(t, events)
			checkLog(t, a)
			require.Len(t, got, 1)
			assert.Equal(t, tt.want.ID, got[0].ID)
			assert.Equal(t, tt.want.Name, got[0].Name)
			assert.JSONEq(t, string(tt.want.Input), string(got[0].Input))
			assert.Equal(t, tt.reason, reasons[0], "the request has the reason of the CLI")
		})
	}
}

func TestRunNotLoggedIn(t *testing.T) {
	useTraces(t, []string{"unauthorized.trace"})
	a := newAgent(t)

	events, _, err := collect(a.Run(context.Background(), session.Text("Hi.")))
	require.ErrorIs(t, err, ErrAuth)
	assert.ErrorContains(t, err, "401 Unauthorized")
	emittest.CheckEvents(t, events)
}

func TestApproveBeforeItem(t *testing.T) {
	tests := []struct {
		name string
		file string
		want []ai.ToolCall
	}{
		{
			name: "a command comes from the request",
			file: "approval_before_item.trace",
			want: []ai.ToolCall{{
				ID:    "c1",
				Name:  "bash",
				Input: json.RawMessage(`{"command":"rm -rf /tmp/x"}`),
			}},
		},
		{
			name: "a file change with no item is declined without a question",
			file: "approval_file_before_item.trace",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			useTraces(t, []string{tt.file})
			var got []ai.ToolCall
			a := newAgent(t,
				Policy(ApprovalUntrusted),
				Approve(func(ctx context.Context, req agent.ApprovalRequest) (agent.Decision, error) {
					got = append(got, req.Call)
					return agent.Decision{Allow: true}, nil
				}),
			)

			events, _, err := collect(a.Run(context.Background(), session.Text("Remove /tmp/x.")))
			require.NoError(t, err, "the trace matches the decision")
			emittest.CheckEvents(t, events)
			checkLog(t, a)
			require.Len(t, got, len(tt.want))
			for i, want := range tt.want {
				assert.Equal(t, want.ID, got[i].ID)
				assert.Equal(t, want.Name, got[i].Name)
				assert.JSONEq(t, string(want.Input), string(got[i].Input))
			}
		})
	}
}

// TestRecordedApproval runs the real CLI with the approval policy that
// the driver sets for an Approve function. To record it again:
//
//	make record-cli AGENT=codex RUN=TestRecordedApproval
func TestRecordedApproval(t *testing.T) {
	dir := t.TempDir()
	real, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	useTraces(t, []string{"TestRecordedApproval.trace"},
		replaceScrub(jsonString(real), workDir),
		replaceScrub(jsonString(dir), workDir),
		replaceScrub(jsonString(home), "/home/me"),
		machineScrub,
		userHookScrub,
	)
	var asked []string
	a := New(
		Dir(dir),
		Policy(ApprovalUntrusted),
		Thinking(ai.ThinkingLow),
		Approve(func(_ context.Context, req agent.ApprovalRequest) (agent.Decision, error) {
			asked = append(asked, req.Call.Name)
			return agent.Decision{Allow: true}, nil
		}),
	)
	t.Cleanup(func() { a.Close() })

	prompt := "Run the shell command `touch a.txt`. Then reply with the word done."
	events, _, err := collect(a.Run(context.Background(), session.Text(prompt)))
	require.NoError(t, err)
	emittest.CheckEvents(t, events)
	checkLog(t, a)
	assert.Contains(t, asked, "bash", "the policy asks before the command")
}
