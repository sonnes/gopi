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
	"github.com/sonnes/gopi/pkg/def"
	"github.com/sonnes/gopi/pkg/harness/permission"
	"github.com/sonnes/gopi/pkg/session"
	"github.com/sonnes/gopi/pkg/tool"
)

// childCall is the subagent of a child call of the harness.
type childCall struct {
	sub    def.Agent
	parent agent.ParentRef
}

// agentToolName is the name of the agent tool.
const agentToolName = "agent"

// idBytes is the count of random bytes in the ID of a child session.
// The ID shows them as hex digits.
const idBytes = 3

// inheritModel is the model of a subagent that runs on the model of its
// parent.
const inheritModel = "inherit"

const agentToolDescription = "Give a task to a subagent. The subagent works in its own context " +
	"with its own tools, and returns its last message and an agent_id. " +
	"Give the agent_id as resume to continue the same subagent."

// agentTool is the tool that runs a subagent. Each call runs a child
// agent in a child session of the store, with the ID of the parent
// session, ".", the subagent name, "-", and 6 hex digits, as in
// [oh-my-pi]. A child keeps its parent in the [agent.Parent] value, as
// a Codex thread keeps parent_thread_id. The run stream of the parent
// carries the start, each event, and the end of the child, through
// [agent.EmitSubagent]. The result is the last text of the child, its
// agent_id, and its usage, as in [Claude Code].
//
// [oh-my-pi]: https://github.com/can1357/oh-my-pi
// [Claude Code]: https://code.claude.com/docs/en/sub-agents
type agentTool struct {
	asm    *Assembly
	names  []string
	byName map[string]def.Agent
}

// newAgentTool returns the agent tool for the subagents of asm.
func newAgentTool(asm *Assembly) *agentTool {
	t := &agentTool{
		asm:    asm,
		byName: make(map[string]def.Agent, len(asm.Agents)),
	}
	for _, a := range asm.Agents {
		t.names = append(t.names, a.Name)
		t.byName[a.Name] = a
	}
	return t
}

// Info returns the info of the agent tool. The agent input lists the
// subagent names as an enum.
func (t *agentTool) Info() ai.ToolInfo {
	name := &jsonschema.Schema{
		Type:        "string",
		Description: "The name of the subagent.",
	}
	for _, n := range t.names {
		name.Enum = append(name.Enum, n)
	}
	return ai.ToolInfo{
		Name:        agentToolName,
		Description: agentToolDescription,
		InputSchema: &jsonschema.Schema{
			Type: "object",
			Properties: map[string]*jsonschema.Schema{
				"agent": name,
				"description": {
					Type:        "string",
					Description: "A title of the task, in 3 to 5 words. A UI shows it.",
				},
				"prompt": {
					Type:        "string",
					Description: "The task. The subagent reads only this text, so give every fact that it needs.",
				},
				"resume": {
					Type:        "string",
					Description: "The agent_id of an earlier agent call, to continue that subagent.",
				},
			},
			Required: []string{"agent", "prompt"},
		},
	}
}

type agentInput struct {
	Agent       string `json:"agent"`
	Description string `json:"description"`
	Prompt      string `json:"prompt"`
	Resume      string `json:"resume"`
}

// Run runs the subagent of one call to its end. A failure of the input
// or of the child is an error result, so the model can correct the
// call. A canceled context stops the run of the parent.
func (t *agentTool) Run(ctx context.Context, call ai.ToolCall) (ai.ToolResult, error) {
	var in agentInput
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
	spec, err := childSpec(t.asm.h.cat, t.asm.Spec, sub.Model)
	if err != nil {
		return errorResult(fmt.Sprintf("the subagent did not start: %s", err)), nil
	}
	child, err := t.start(ctx, sub, spec, id, call.ID)
	if err != nil {
		return errorResult(fmt.Sprintf("the subagent did not start: %s", err)), nil
	}
	agent.EmitSubagent(ctx, agent.EventSubagentStart{
		SessionID: id,
		Name:      sub.Name,
		Title:     in.Description,
		Prompt:    in.Prompt,
		Model:     spec,
	})
	return t.run(ctx, child, id, in.Prompt)
}

// childID returns the ID of the child sessionID: resume, or a new ID. An
// unsaved parent has unsaved children, so it returns "".
func (t *agentTool) childID(ctx context.Context, sub def.Agent, resumeID string) (string, error) {
	parentID := t.asm.sessionID
	if parentID == "" {
		if resumeID != "" {
			return "", errors.New("this agent keeps no session, so a subagent cannot resume")
		}
		return "", nil
	}
	if resumeID == "" {
		return newChildID(parentID, sub.Name)
	}

	ofParent := strings.HasPrefix(resumeID, parentID+".")
	if !ofParent {
		return "", fmt.Errorf("the agent_id %q is not a subagent of this session", resumeID)
	}
	if _, err := t.asm.h.store.Load(ctx, resumeID); err != nil {
		return "", fmt.Errorf("no subagent has the agent_id %q", resumeID)
	}
	return resumeID, nil
}

// newChildID returns a new ID of a child session of parent.
func newChildID(parentID, name string) (string, error) {
	b := make([]byte, idBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return parentID + "." + name + "-" + hex.EncodeToString(b), nil
}

// start returns the child agent of sub on the model spec, over the
// session id, or over an unsaved log if id is "".
func (t *agentTool) start(ctx context.Context, sub def.Agent, spec, id, callID string) (*agent.Agent, error) {
	h := t.asm.h
	mode, err := permission.ParseMode(sub.Mode)
	if err != nil {
		mode = h.policy.Mode
	}
	opts := []Option{
		func(c *Harness) {
			c.sections = nil
			c.child = &childCall{
				sub: sub,
				parent: agent.ParentRef{
					SessionID:  t.asm.sessionID,
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
func (t *agentTool) childSystem(sub def.Agent) string {
	parts := []string{sub.Prompt}
	for _, name := range sub.Skills {
		s, ok := t.asm.skill(name)
		if ok {
			parts = append(parts, skillText(name, s))
		}
	}
	return strings.Join(parts, "\n\n")
}

// run runs child with prompt, sends each event and the end of the
// child on the run stream of the parent, and returns the result of the
// agent call. The end comes before the call ends, also when ctx is done,
// so the stream of the parent closes each child that it opened.
func (t *agentTool) run(
	ctx context.Context,
	child *agent.Agent,
	id string,
	prompt string,
) (ai.ToolResult, error) {
	start := time.Now()
	toolUses := 0

	s := child.Run(ctx, session.Text(prompt))
	for e, err := range s.Events() {
		if err != nil {
			break
		}
		if _, ok := e.(agent.EventToolExecutionEnd); ok {
			toolUses++
		}
		agent.EmitSubagent(ctx, agent.EventSubagent{
			SessionID: id,
			Event:     e,
		})
	}
	msgs, err := s.Wait()
	end := agent.EventSubagentEnd{
		SessionID: id,
		Status:    agent.SubagentCompleted,
		Text:      lastText(msgs),
	}
	switch {
	case ctx.Err() != nil:
		end.Status = agent.SubagentInterrupted
	case err != nil:
		end.Status = agent.SubagentFailed
		end.Text = err.Error()
	}
	agent.EmitSubagent(ctx, end)
	if ctx.Err() != nil {
		return ai.ToolResult{}, ctx.Err()
	}

	u := child.State().Usage()
	stats := agentStats{
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

// agentStats is the end of the result of an agent call.
type agentStats struct {
	id       string
	tokens   int
	toolUses int
	duration time.Duration
}

// trailer returns the agent_id line and the usage line.
func (s agentStats) trailer() string {
	var b strings.Builder
	b.WriteString("\n\n")
	if s.id != "" {
		fmt.Fprintf(&b, "agent_id: %s (give it to agent as resume to continue)\n", s.id)
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
func keepTools(tools []tool.Tool, sub def.Agent) []tool.Tool {
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
