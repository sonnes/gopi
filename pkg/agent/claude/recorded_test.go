package claude_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/internal/stdiorr"
	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/claude"
	"github.com/sonnes/gopi/pkg/agent/internal/emittest"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
)

// scrubLine keeps a recorded line small and free of the data of the
// machine that recorded it: the initialize response lists the account,
// and the system lines list the paths, tools, and plugins of the CLI.
// A task line of a subagent keeps the fields that the agent reads.
func scrubLine(line []byte) []byte {
	var m map[string]any
	if err := json.Unmarshal(line, &m); err != nil {
		return line
	}
	switch m["type"] {
	case "system":
		kept := map[string]any{
			"type":       m["type"],
			"subtype":    m["subtype"],
			"session_id": m["session_id"],
		}
		subtype, _ := m["subtype"].(string)
		if strings.HasPrefix(subtype, "task_") {
			for _, k := range taskFields {
				if v, ok := m[k]; ok {
					kept[k] = v
				}
			}
		}
		m = kept
	case "rate_limit_event":
		m = map[string]any{"type": m["type"]}
	case "control_response":
		resp, _ := m["response"].(map[string]any)
		if resp == nil || resp["request_id"] != "req_1" {
			return line
		}
		m = map[string]any{
			"type": "control_response",
			"response": map[string]any{
				"subtype":    resp["subtype"],
				"request_id": resp["request_id"],
				"response":   map[string]any{},
			},
		}
	default:
		return line
	}
	out, err := json.Marshal(m)
	if err != nil {
		return line
	}
	return out
}

// taskFields are the fields of a task line that the agent reads.
var taskFields = []string{
	"task_id",
	"tool_use_id",
	"task_type",
	"description",
	"prompt",
	"status",
	"summary",
}

// scrubDir replaces dir with a fixed value.
func scrubDir(dir, with string) stdiorr.Scrub {
	return func(line []byte) []byte {
		if dir == "" {
			return line
		}
		return bytes.ReplaceAll(line, []byte(dir), []byte(with))
	}
}

// TestRecorded runs the real CLI. Record it with
// `make record-cli AGENT=claude RUN=TestRecorded`.
func TestRecorded(t *testing.T) {
	dir := t.TempDir()
	home, _ := os.UserHomeDir()
	claude.Replay(t, []stdiorr.Scrub{
		scrubLine,
		// macOS reports a temporary directory under /private.
		scrubDir("/private"+dir, "/work"),
		scrubDir(dir, "/work"),
		scrubDir(home, "/home/me"),
	}, "testdata/TestRecorded.trace")

	a := claude.New(
		claude.Dir(dir),
		claude.Model("haiku"),
		claude.PermissionMode(claude.PermissionDontAsk),
	)
	defer closeAgent(t, a)

	events, msgs, err := run(context.Background(), a, "Reply with the word hi and nothing else.")
	require.NoError(t, err)
	emittest.CheckEvents(t, events)

	require.NotEmpty(t, msgs)
	last := msgs[len(msgs)-1]
	assert.Equal(t, ai.RoleAssistant, last.Role)
	assert.Contains(t, strings.ToLower(last.Text()), "hi")
	checkLog(t, a)

	id, err := claude.SessionID(a)
	require.NoError(t, err)
	assert.NotEmpty(t, id)
}

// TestRecordedHooks runs the real CLI with a BeforeTool hook and a
// BeforeStop hook. Record it with
// `make record-cli AGENT=claude RUN=TestRecordedHooks`.
func TestRecordedHooks(t *testing.T) {
	dir := t.TempDir()
	home, _ := os.UserHomeDir()
	claude.Replay(t, []stdiorr.Scrub{
		scrubLine,
		scrubDir("/private"+dir, "/work"),
		scrubDir(dir, "/work"),
		scrubDir(home, "/home/me"),
		// The transcript path of a hook input names the folder of the
		// session after dir, with each slash as a dash.
		scrubDir(strings.ReplaceAll("/private"+dir, "/", "-"), "-work"),
	}, "testdata/TestRecordedHooks.trace")

	var seen []ai.ToolCall
	guard := toolGuard{
		seen: &seen,
		fn: func(c ai.ToolCall) (ai.ToolCall, error) {
			return c, agent.Deny("Do not delete files.")
		},
	}
	stops := 0
	a := claude.New(
		claude.Dir(dir),
		claude.Model("haiku"),
		claude.PermissionMode(claude.PermissionDontAsk),
		claude.Hooks(guard, goodbye{calls: &stops}),
	)
	defer closeAgent(t, a)

	prompt := "Run the shell command `rm a.txt` with the Bash tool. Then reply with the word done."
	events, msgs, err := run(context.Background(), a, prompt)
	require.NoError(t, err)
	emittest.CheckEvents(t, events)

	require.NotEmpty(t, seen, "the CLI calls the PreToolUse hook")
	assert.Equal(t, "Bash", seen[0].Name)
	var results []string
	for _, m := range msgs {
		if m.Role == ai.RoleToolResult {
			results = append(results, m.Text())
		}
	}
	require.NotEmpty(t, results)
	assert.Contains(t, results[0], "Do not delete files.")
	assert.Equal(t, 2, stops, "the first Stop continues the run")
	feedback := slices.ContainsFunc(a.Log().Messages(), func(m ai.Message) bool {
		return m.Role == ai.RoleUser && strings.Contains(m.Text(), "Also say goodbye.")
	})
	assert.True(t, feedback, "the log keeps the feedback of the Stop hook")
	checkLog(t, a)
}

// outputEditor is a hook at AfterTool. It records each result. It
// replaces "two" with "three" in the stdout of a Bash result, and the
// result of a failed call with a note.
type outputEditor struct {
	agent.BaseHook
	results *[]ai.ToolResult
}

func (h outputEditor) AfterTool(
	_ context.Context,
	_ agent.State,
	_ ai.ToolCall,
	r ai.ToolResult,
) (ai.ToolResult, error) {
	*h.results = append(*h.results, r)
	if r.IsError {
		return ai.ToolResult{
			Content: []ai.Content{ai.Text{Text: "The folder nope does not exist."}},
			IsError: true,
		}, nil
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(toolText(r)), &out); err != nil {
		return r, nil
	}
	stdout, _ := out["stdout"].(string)
	out["stdout"] = strings.ReplaceAll(stdout, "two", "three")
	data, err := json.Marshal(out)
	if err != nil {
		return r, err
	}
	return ai.ToolResult{Content: []ai.Content{ai.Text{Text: string(data)}}}, nil
}

// TestRecordedToolEdits runs the real CLI with a BeforeTool hook that
// changes the input of a Bash call, and an AfterTool hook that changes
// the output. Record it with
// `make record-cli AGENT=claude RUN=TestRecordedToolEdits`.
func TestRecordedToolEdits(t *testing.T) {
	dir := t.TempDir()
	home, _ := os.UserHomeDir()
	claude.Replay(t, []stdiorr.Scrub{
		scrubLine,
		scrubDir("/private"+dir, "/work"),
		scrubDir(dir, "/work"),
		scrubDir(home, "/home/me"),
		scrubDir(strings.ReplaceAll("/private"+dir, "/", "-"), "-work"),
	}, "testdata/TestRecordedToolEdits.trace")

	var seen []ai.ToolCall
	guard := toolGuard{
		seen: &seen,
		fn: func(c ai.ToolCall) (ai.ToolCall, error) {
			var in map[string]any
			if err := json.Unmarshal(c.Input, &in); err != nil {
				return c, err
			}
			if in["command"] != "echo one" {
				return c, nil
			}
			in["command"] = "echo two"
			data, err := json.Marshal(in)
			c.Input = data
			return c, err
		},
	}
	var results []ai.ToolResult
	a := claude.New(
		claude.Dir(dir),
		claude.Model("haiku"),
		claude.PermissionMode(claude.PermissionDontAsk),
		claude.AllowedTools("Bash"),
		claude.Hooks(guard, outputEditor{results: &results}),
	)
	defer closeAgent(t, a)

	prompt := "Make two separate Bash tool calls, one after the other. " +
		"First run `echo one`. Then run `ls nope`. " +
		"Then reply with the exact stdout of the first command and nothing else."
	events, msgs, err := run(context.Background(), a, prompt)
	require.NoError(t, err)
	emittest.CheckEvents(t, events)
	checkLog(t, a)

	require.NotEmpty(t, seen, "the CLI calls the PreToolUse hook")
	require.NotEmpty(t, results, "the CLI calls the PostToolUse hook")
	assert.Contains(t, toolText(results[0]), `"stdout":"two`,
		"the CLI runs the input of the BeforeTool hook")
	var outputs []string
	for _, m := range msgs {
		if m.Role == ai.RoleToolResult {
			outputs = append(outputs, m.Text())
		}
	}
	require.NotEmpty(t, outputs)
	assert.Contains(t, outputs[0], "three", "the model reads the output of the AfterTool hook")
	require.Len(t, results, 2)
	assert.True(t, results[1].IsError, "the CLI calls PostToolUseFailure for a failed call")
}

// TestRecordedSubagent runs the real CLI with one call of the Agent
// tool. Record it with
// `make record-cli AGENT=claude RUN=TestRecordedSubagent`.
func TestRecordedSubagent(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	home, _ := os.UserHomeDir()
	require.NoError(t, os.WriteFile(dir+"/word.txt", []byte("banana\n"), 0o600))
	claude.Replay(t, []stdiorr.Scrub{
		scrubLine,
		scrubDir("/private"+dir, "/work"),
		scrubDir(dir, "/work"),
		scrubDir(home, "/home/me"),
	}, "testdata/TestRecordedSubagent.trace")

	store := session.NewMemoryStore()
	a, err := claude.Durable(ctx, "s1",
		claude.Dir(dir),
		claude.Model("haiku"),
		claude.PermissionMode(claude.PermissionBypass),
		claude.Store(store),
	)
	require.NoError(t, err)
	defer closeAgent(t, a)

	prompt := "Use the Agent tool exactly once, with subagent_type general-purpose, " +
		"to read the file word.txt and report the word in it. Then reply with that word only."
	events, msgs, err := run(ctx, a, prompt)
	require.NoError(t, err)
	emittest.CheckEvents(t, events)

	var (
		starts []agent.EventSubagentStart
		ends   []agent.EventSubagentEnd
		inner  []agent.Event
	)
	for _, e := range events {
		switch e := e.(type) {
		case agent.EventSubagentStart:
			starts = append(starts, e)
		case agent.EventSubagentEnd:
			ends = append(ends, e)
		case agent.EventSubagent:
			inner = append(inner, e.Event)
		}
	}
	require.Len(t, starts, 1, "one Agent call starts one child")
	require.Len(t, ends, 1)
	start := starts[0]
	assert.Equal(t, "general-purpose", start.Name)
	assert.Regexp(t, `^s1\.general-purpose-\S+$`, start.SessionID)
	assert.NotEmpty(t, start.Prompt)
	assert.Equal(t, start.ToolCallID, ends[0].ToolCallID)
	assert.Equal(t, agent.SubagentCompleted, ends[0].Status)
	assert.Contains(t, strings.ToLower(ends[0].Text), "banana")
	emittest.CheckEvents(t, inner)

	snap, err := store.Load(ctx, start.SessionID)
	require.NoError(t, err)
	parent, ok, err := agent.Parent.Read(snap)
	require.NoError(t, err)
	require.True(t, ok, "the child names its parent")
	assert.Equal(t, agent.ParentRef{SessionID: "s1", ToolCallID: start.ToolCallID}, parent)
	assert.NotEmpty(t, snap.Entries, "the child session keeps the messages of the child")

	for _, m := range a.Log().Messages() {
		if m.Role == ai.RoleUser {
			assert.NotContains(t, m.Text(), start.Prompt, "the parent log does not keep the child")
		}
	}
	require.NotEmpty(t, msgs)
	assert.Contains(t, strings.ToLower(msgs[len(msgs)-1].Text()), "banana")
	checkLog(t, a)
}
