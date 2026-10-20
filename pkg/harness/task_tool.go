package harness

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/catalog"
	"github.com/sonnes/gopi/pkg/harness/permission"
	"github.com/sonnes/gopi/pkg/harness/subagent"
	"github.com/sonnes/gopi/pkg/session"
	"github.com/sonnes/gopi/pkg/tool"
)

// Parent is the session value of a child session that names its
// parent. A UI reads it to find the children of a session. The harness
// writes it when it creates the child; a caller does not.
var Parent = session.Value[ParentRef]{
	Namespace: "gopi.harness",
	Key:       "parent",
}

// ParentRef names the session and the task call that started a child
// session.
type ParentRef struct {
	// Session is the ID of the parent session.
	Session string `json:"session"`
	// ToolCallID is the ID of the call of the task tool.
	ToolCallID string `json:"tool_call_id"`
}

// childCall is the subagent of a child call of the harness.
type childCall struct {
	sub    subagent.Agent
	parent ParentRef
}

// taskToolName is the name of the task tool.
const taskToolName = "task"

// idBytes is the count of random bytes in the ID of a child session.
// The ID shows them as hex digits.
const idBytes = 3

// inheritModel is the model of a subagent that runs on the model of its
// parent.
const inheritModel = "inherit"

const taskToolDescription = "Give a task to a subagent. The subagent works in its own context " +
	"with its own tools, and returns its last message and an agent_id. " +
	"Give the agent_id as resume to continue the same subagent."

// taskTool is the tool that runs a subagent. Each call runs a child
// agent in a child session of the store, with the ID of the parent
// session, ".", the subagent name, "-", and 6 hex digits, as in
// [oh-my-pi]. A child keeps its parent in the [Parent] value, as a
// Codex thread keeps parent_thread_id. The result is the last text of
// the child, its agent_id, and its usage, as in [Claude Code].
//
// [oh-my-pi]: https://github.com/can1357/oh-my-pi
// [Claude Code]: https://code.claude.com/docs/en/sub-agents
type taskTool struct {
	asm    *Assembly
	names  []string
	byName map[string]subagent.Agent
}

// newTaskTool returns the task tool for the subagents of asm.
func newTaskTool(asm *Assembly) *taskTool {
	t := &taskTool{
		asm:    asm,
		byName: make(map[string]subagent.Agent, len(asm.Agents)),
	}
	for _, a := range asm.Agents {
		t.names = append(t.names, a.Name)
		t.byName[a.Name] = a
	}
	return t
}

// Info returns the info of the task tool. The agent input lists the
// subagent names as an enum.
func (t *taskTool) Info() ai.ToolInfo {
	name := &jsonschema.Schema{
		Type:        "string",
		Description: "The name of the subagent.",
	}
	for _, n := range t.names {
		name.Enum = append(name.Enum, n)
	}
	return ai.ToolInfo{
		Name:        taskToolName,
		Description: taskToolDescription,
		InputSchema: &jsonschema.Schema{
			Type: "object",
			Properties: map[string]*jsonschema.Schema{
				"agent": name,
				"prompt": {
					Type:        "string",
					Description: "The task. The subagent reads only this text, so give every fact that it needs.",
				},
				"resume": {
					Type:        "string",
					Description: "The agent_id of an earlier task call, to continue that subagent.",
				},
			},
			Required: []string{"agent", "prompt"},
		},
	}
}

type taskInput struct {
	Agent  string `json:"agent"`
	Prompt string `json:"prompt"`
	Resume string `json:"resume"`
}

// Run runs the subagent of one call to its end. A failure of the input
// or of the child is an error result, so the model can correct the
// call. A canceled context stops the run of the parent.
func (t *taskTool) Run(ctx context.Context, call ai.ToolCall) (ai.ToolResult, error) {
	var in taskInput
	if err := json.Unmarshal(call.Input, &in); err != nil {
		return errorResult(fmt.Sprintf("invalid input: %s", err)), nil
	}
	sub, ok := t.byName[in.Agent]
	if !ok {
		text := fmt.Sprintf("no subagent is named %q. The subagents are: %s.", in.Agent, strings.Join(t.names, ", "))
		return errorResult(text), nil
	}
	if strings.TrimSpace(in.Prompt) == "" {
		return errorResult("the prompt is empty. Give the task as prompt."), nil
	}

	id, err := t.childID(ctx, sub, in.Resume)
	if err != nil {
		return errorResult(err.Error()), nil
	}
	child, err := t.start(ctx, sub, id, call.ID)
	if err != nil {
		return errorResult(fmt.Sprintf("the subagent did not start: %s", err)), nil
	}
	return t.run(ctx, child, id, call.ID, in.Prompt)
}

// childID returns the ID of the child session: resume, or a new ID. An
// unsaved parent has unsaved children, so it returns "".
func (t *taskTool) childID(ctx context.Context, sub subagent.Agent, resume string) (string, error) {
	parent := t.asm.session
	if parent == "" {
		if resume != "" {
			return "", errors.New("this agent keeps no session, so a subagent cannot resume")
		}
		return "", nil
	}
	if resume == "" {
		return newChildID(parent, sub.Name)
	}

	ofParent := strings.HasPrefix(resume, parent+".")
	if !ofParent {
		return "", fmt.Errorf("the agent_id %q is not a subagent of this session", resume)
	}
	if _, err := t.asm.h.store.Load(ctx, resume); err != nil {
		return "", fmt.Errorf("no subagent has the agent_id %q", resume)
	}
	return resume, nil
}

// newChildID returns a new ID of a child session of parent.
func newChildID(parent, name string) (string, error) {
	b := make([]byte, idBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return parent + "." + name + "-" + hex.EncodeToString(b), nil
}

// start returns the child agent of sub over the session id, or over an
// unsaved log if id is "".
func (t *taskTool) start(ctx context.Context, sub subagent.Agent, id, callID string) (*agent.Agent, error) {
	h := t.asm.h
	mode, err := permission.ParseMode(sub.Mode)
	if err != nil {
		mode = h.policy.Mode
	}
	spec, err := childSpec(h.cat, t.asm.Spec, sub.Model)
	if err != nil {
		return nil, err
	}
	opts := []Option{
		func(c *Harness) {
			c.child = &childCall{
				sub: sub,
				parent: ParentRef{
					Session:    t.asm.session,
					ToolCallID: callID,
				},
			}
		},
		Model(spec),
		System(t.childSystem(sub)),
		Mode(permission.Stricter(h.policy.Mode, mode)),
	}
	if id == "" {
		return h.New(ctx, opts...)
	}
	return h.Durable(ctx, id, opts...)
}

// childSystem returns the base text of the prompt of a child: the prompt
// of sub, then the body of each skill that sub preloads.
func (t *taskTool) childSystem(sub subagent.Agent) string {
	parts := []string{sub.Prompt}
	for _, name := range sub.Skills {
		s, ok := t.asm.skill(name)
		if ok {
			parts = append(parts, skillText(name, s))
		}
	}
	return strings.Join(parts, "\n\n")
}

// run runs child with prompt, sends each event to the SubagentEvents
// function, and returns the result of the task call.
func (t *taskTool) run(
	ctx context.Context,
	child *agent.Agent,
	id string,
	callID string,
	prompt string,
) (ai.ToolResult, error) {
	start := time.Now()
	send := t.asm.h.subagentEvents
	toolUses := 0

	s := child.Run(ctx, session.Text(prompt))
	for e, err := range s.Events() {
		if err != nil {
			break
		}
		if _, ok := e.(agent.EventToolExecutionEnd); ok {
			toolUses++
		}
		if send != nil {
			send(callID, e)
		}
	}
	msgs, err := s.Wait()
	if ctx.Err() != nil {
		return ai.ToolResult{}, ctx.Err()
	}

	u := child.State().Usage()
	stats := taskStats{
		id:       id,
		tokens:   u.Input + u.Output + u.CacheRead + u.CacheWrite,
		toolUses: toolUses,
		duration: time.Since(start),
	}
	if err != nil {
		return errorResult(fmt.Sprintf("the subagent failed: %s", err) + stats.trailer()), nil
	}
	return textResult(lastText(msgs) + stats.trailer()), nil
}

// taskStats is the end of a task result.
type taskStats struct {
	id       string
	tokens   int
	toolUses int
	duration time.Duration
}

// trailer returns the agent_id line and the usage line.
func (s taskStats) trailer() string {
	var b strings.Builder
	b.WriteString("\n\n")
	if s.id != "" {
		fmt.Fprintf(&b, "agent_id: %s (give it to task as resume to continue)\n", s.id)
	}
	fmt.Fprintf(
		&b,
		"<usage>total_tokens: %d, tool_uses: %d, duration_ms: %d</usage>",
		s.tokens,
		s.toolUses,
		s.duration.Milliseconds(),
	)
	return b.String()
}

// lastText returns the text of the last assistant message with text.
func lastText(msgs []ai.Message) string {
	for _, m := range slices.Backward(msgs) {
		if m.Role != ai.RoleAssistant {
			continue
		}
		if text := m.Text(); text != "" {
			return text
		}
	}
	return "(The subagent ended with no text.)"
}

// childSpec returns the model spec of a subagent with the model key
// file. "inherit", no model, or a model that cat does not know, such as
// the alias "opus", gives the spec of the parent. Any other error of cat
// is an error, such as a bare ID that two providers serve. The child
// then does not run on a model that its file did not name.
func childSpec(cat *catalog.Catalog, parent, file string) (string, error) {
	if file == "" || file == inheritModel {
		return parent, nil
	}
	_, err := cat.Model(file)
	if errors.Is(err, catalog.ErrUnknownModel) {
		return parent, nil
	}
	if err != nil {
		return "", err
	}
	return file, nil
}

// toolAliases maps the tool names of a Claude Code subagent file to the
// names of the gopi tools.
var toolAliases = map[string][]string{
	"Read":      {"read_file"},
	"Write":     {"write_file"},
	"Edit":      {"edit_file"},
	"MultiEdit": {"edit_file"},
	"Bash":      {"bash"},
	"Grep":      {"grep"},
	"Glob":      {"find"},
	"TodoWrite": {"todo_write"},
	"Skill":     {"skill"},
}

// keepTools returns the tools that sub can call: the tools of its tools
// key, or all of them if it has none, without the tools of its
// disallowedTools key.
func keepTools(tools []tool.Tool, sub subagent.Agent) []tool.Tool {
	allowed := toolNames(sub.Tools)
	denied := toolNames(sub.Disallowed)
	var out []tool.Tool
	for _, t := range tools {
		name := t.Info().Name
		if denied[name] {
			continue
		}
		if sub.Tools != nil && !allowed[name] {
			continue
		}
		out = append(out, t)
	}
	return out
}

// toolNames returns the gopi names of the tool names of a subagent file.
func toolNames(names []string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[n] = true
		for _, alias := range toolAliases[n] {
			out[alias] = true
		}
	}
	return out
}
