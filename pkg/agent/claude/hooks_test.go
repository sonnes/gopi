package claude_test

import (
	"context"
	"encoding/json"
	"errors"
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

// toolGuard is a hook at BeforeTool. It records each call and returns
// the call and the error of fn.
type toolGuard struct {
	agent.BaseHook
	seen *[]ai.ToolCall
	fn   func(ai.ToolCall) (ai.ToolCall, error)
}

func (h toolGuard) BeforeTool(_ context.Context, _ agent.State, c ai.ToolCall) (ai.ToolCall, error) {
	*h.seen = append(*h.seen, c)
	return h.fn(c)
}

// resultEditor is a hook at AfterTool and AfterTurn. It records the
// text of each result and replaces the text with the text of edit.
type resultEditor struct {
	agent.BaseHook
	results *[]string
	edit    func(string) string
}

func (h resultEditor) AfterTool(
	_ context.Context,
	_ agent.State,
	_ ai.ToolCall,
	r ai.ToolResult,
) (ai.ToolResult, error) {
	*h.results = append(*h.results, toolText(r))
	return ai.ToolResult{Content: []ai.Content{ai.Text{Text: h.edit(toolText(r))}}}, nil
}

func (resultEditor) AfterTurn(context.Context, agent.State) ([]session.Entry, error) {
	return []session.Entry{session.Text("Check the tests next.")}, nil
}

// toolText returns the text of the blocks of r.
func toolText(r ai.ToolResult) string {
	var out string
	for _, c := range r.Content {
		if t, ok := c.(ai.Text); ok {
			out += t.Text
		}
	}
	return out
}

// goodbye is a hook at BeforeStop. It continues the run once. If
// states is set, it records the state of each call.
type goodbye struct {
	agent.BaseHook
	calls  *int
	states *[]agent.State
}

func (h goodbye) BeforeStop(_ context.Context, s agent.State) ([]session.Entry, error) {
	if h.states != nil {
		*h.states = append(*h.states, s)
	}
	*h.calls++
	if *h.calls > 1 {
		return nil, nil
	}
	return []session.Entry{session.Text("Also say goodbye.")}, nil
}

// failingStop is a hook at BeforeStop that fails.
type failingStop struct{ agent.BaseHook }

func (failingStop) BeforeStop(context.Context, agent.State) ([]session.Entry, error) {
	return nil, errors.New("the check failed")
}

// callHook is a hook at BeforeCall, a point that the CLI does not have.
type callHook struct{ agent.BaseHook }

func (callHook) BeforeCall(_ context.Context, _ agent.State, r agent.Request) (agent.Request, error) {
	return r, nil
}

func TestHookBeforeTool(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		fn      func(ai.ToolCall) (ai.ToolCall, error)
		result  string
		isError bool
	}{
		{
			name: "a deny blocks the call",
			file: "testdata/hook_pre_tool.trace",
			fn: func(c ai.ToolCall) (ai.ToolCall, error) {
				return c, agent.Deny("Do not delete files.")
			},
			result:  "Do not delete files.",
			isError: true,
		},
		{
			name: "a new input replaces the input",
			file: "testdata/hook_pre_tool_input.trace",
			fn: func(c ai.ToolCall) (ai.ToolCall, error) {
				c.Input = json.RawMessage(`{"command":"rm -i a.txt"}`)
				return c, nil
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claude.Replay(t, nil, tt.file)
			var seen []ai.ToolCall
			a := claude.New(claude.Hooks(toolGuard{seen: &seen, fn: tt.fn}))
			defer closeAgent(t, a)

			_, msgs, err := run(context.Background(), a, "Delete a.txt")
			require.NoError(t, err, "the trace matches the answer of the hook")

			require.Len(t, seen, 1)
			assert.Equal(t, "toolu_1", seen[0].ID)
			assert.Equal(t, "Bash", seen[0].Name)
			assert.JSONEq(t, `{"command":"rm a.txt"}`, string(seen[0].Input))
			require.Len(t, msgs, 3)
			assert.Equal(t, tt.result, msgs[1].Text())
			assert.Equal(t, tt.isError, msgs[1].IsError)
		})
	}
}

func TestHookAfterToolAndAfterTurn(t *testing.T) {
	tests := []struct {
		name string
		file string
		// seen is the text of the result that the hook gets.
		seen string
		edit func(string) string
	}{
		{
			name: "the new JSON of a built-in tool goes to the CLI as JSON",
			file: "testdata/hook_post_tool.trace",
			seen: `{"stdout":"a.txt","stderr":"","interrupted":false}`,
			edit: func(s string) string {
				return strings.Replace(s, `"a.txt"`, `"files: a.txt"`, 1)
			},
		},
		{
			name: "the new text of a text response goes to the CLI as text",
			file: "testdata/hook_post_tool_text.trace",
			seen: "a.txt",
			edit: func(s string) string { return "files: " + s },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claude.Replay(t, nil, tt.file)
			var results []string
			a := claude.New(claude.Hooks(resultEditor{
				results: &results,
				edit:    tt.edit,
			}))
			defer closeAgent(t, a)

			_, msgs, err := run(context.Background(), a, "List the files")
			require.NoError(t, err, "the trace matches the answers of the hooks")
			assert.Equal(t, []string{tt.seen}, results)
			require.Len(t, msgs, 3)
			assert.Equal(t, "files: a.txt", msgs[1].Text())
		})
	}
}

// failureNote is a hook at AfterTool. It records each result and
// replaces the result of a failed call with a note.
type failureNote struct {
	agent.BaseHook
	results *[]ai.ToolResult
}

func (h failureNote) AfterTool(
	_ context.Context,
	_ agent.State,
	_ ai.ToolCall,
	r ai.ToolResult,
) (ai.ToolResult, error) {
	*h.results = append(*h.results, r)
	return ai.ToolResult{
		Content: []ai.Content{ai.Text{Text: "The folder nope does not exist."}},
		IsError: true,
	}, nil
}

func TestHookAfterToolOnAFailedCall(t *testing.T) {
	claude.Replay(t, nil, "testdata/hook_post_tool_failure.trace")
	var results []ai.ToolResult
	a := claude.New(claude.Hooks(failureNote{results: &results}))
	defer closeAgent(t, a)

	_, msgs, err := run(context.Background(), a, "List the files")
	require.NoError(t, err, "the new text goes to the CLI as context")
	require.Len(t, results, 1)
	assert.True(t, results[0].IsError)
	assert.Equal(t, "Exit code 1\nls: nope: No such file or directory", toolText(results[0]))
	require.Len(t, msgs, 3)
	assert.Equal(t,
		"Exit code 1\nls: nope: No such file or directory",
		msgs[1].Text(),
		"the CLI keeps the output of a failed call",
	)
}

func TestHookBeforeStop(t *testing.T) {
	claude.Replay(t, nil, "testdata/hook_stop.trace")
	calls := 0
	var states []agent.State
	a := claude.New(claude.Hooks(goodbye{
		calls:  &calls,
		states: &states,
	}))
	defer closeAgent(t, a)

	events, msgs, err := run(context.Background(), a, "Say hello")
	require.NoError(t, err, "the trace matches the answers of the hook")
	emittest.CheckEvents(t, events)
	assert.Equal(t, 2, calls)
	require.NotEmpty(t, msgs)
	assert.Equal(t, "Goodbye", msgs[len(msgs)-1].Text())

	const feedback = "Stop hook feedback:\nAlso say goodbye."
	var log []string
	for _, e := range a.Log().Entries() {
		log = append(log, string(e.Message.Role)+": "+e.Message.Text())
	}
	assert.Equal(t,
		[]string{
			"user: Say hello",
			"assistant: Hello",
			"user: " + feedback,
			"assistant: Goodbye",
		},
		log,
		"the log keeps the feedback that the CLI adds after a Stop block",
	)
	require.Len(t, states, 2)
	second := states[1].Messages()
	require.Len(t, second, 4)
	assert.Equal(t, feedback, second[2].Text(), "the second hook call sees the feedback")
	checkLog(t, a)
}

func TestHookErrorFailsTheRun(t *testing.T) {
	claude.Replay(t, nil, "testdata/hook_error.trace")
	a := claude.New(claude.Hooks(failingStop{}))
	defer closeAgent(t, a)

	_, _, err := run(context.Background(), a, "Say hello")
	assert.EqualError(t, err, "claude: the hook at Stop: the check failed")
}

func TestHookAtAModelCallFails(t *testing.T) {
	claude.Replay(t, nil)
	a := claude.New(claude.Hooks(callHook{}))
	defer closeAgent(t, a)

	_, _, err := run(context.Background(), a, "Say hello")
	assert.ErrorIs(t, err, errors.ErrUnsupported)
}
