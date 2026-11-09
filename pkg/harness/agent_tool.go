package harness

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/catalog"
	"github.com/sonnes/gopi/pkg/def"
	"github.com/sonnes/gopi/pkg/permission"
	"github.com/sonnes/gopi/pkg/session"
	"github.com/sonnes/gopi/pkg/tool"
)

// childCall is the subagent of a child call of the harness.
type childCall struct {
	sub    def.Agent
	parent agent.ParentRef
}

// idBytes is the count of random bytes in the ID of a child session.
// The ID shows them as hex digits.
const idBytes = 3

// noText is the tool result of a subagent that ended with no text, so
// that the model does not read an empty result as a failure.
const noText = "(The subagent ended with no text.)"

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
	asm   *Assembly
	names []string
}

// newAgentTool returns the agent tool for the subagents of asm.
func newAgentTool(asm *Assembly) *agentTool {
	t := &agentTool{asm: asm}
	for _, a := range asm.Agents {
		t.names = append(t.names, a.Name)
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
		Name:        tool.Agent,
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
	sub, ok := t.asm.defs.Agent(in.Agent)
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
	spec, err := childSpec(t.asm.h.cat, t.asm.Spec, sub)
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

	snap, err := t.asm.h.store.Load(ctx, resumeID)
	if err != nil {
		return "", fmt.Errorf("no subagent has the agent_id %q", resumeID)
	}
	parent, ok, err := agent.Parent.Read(snap)
	ofParent := err == nil && ok && parent.SessionID == parentID
	if !ofParent {
		return "", fmt.Errorf("the agent_id %q is not a subagent of this session", resumeID)
	}
	return resumeID, nil
}

// newChildID returns a new ID of a child session of parent.
func newChildID(parentID, name string) (string, error) {
	b := make([]byte, idBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return agent.ChildID(parentID, name, hex.EncodeToString(b)), nil
}

// childMode returns the mode of a subagent. parent is the mode of the
// parent, and file is the mode that the file of the subagent sets. If
// file is empty or unknown, the child gets the mode of the parent.
// Otherwise the child gets the stricter mode, so a file cannot give
// more access than the parent has.
func childMode(parent permission.Mode, file string) permission.Mode {
	if file == "" {
		return parent
	}
	mode, err := permission.ParseMode(file)
	if err != nil {
		return parent
	}
	return permission.Stricter(parent, mode)
}

// start returns the child agent of sub on the model spec, over the
// session id, or over an unsaved log if id is "".
func (t *agentTool) start(ctx context.Context, sub def.Agent, spec, id, callID string) (*agent.Agent, error) {
	h := t.asm.h
	c := h.cfg
	c.SessionID = id
	c.Spec = spec
	c.Mode = childMode(h.cfg.Mode, sub.Mode)
	c.Sections = nil
	call, err := h.call(c)
	if err != nil {
		return nil, err
	}
	call.system = t.childSystem(sub)
	call.child = &childCall{
		sub: sub,
		parent: agent.ParentRef{
			SessionID:  t.asm.sessionID,
			ToolCallID: callID,
		},
	}
	if id == "" {
		return call.newAgent(ctx)
	}
	return call.start(ctx)
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
	text := agent.LastText(msgs)
	end := agent.EventSubagentEnd{
		SessionID: id,
		Status:    agent.SubagentCompleted,
		Text:      text,
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
	if text == "" {
		text = noText
	}
	return textResult(text + stats.trailer()), nil
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

// childSpec returns the model spec of the subagent sub. A model that
// [def.Agent.InheritsModel] reports, or a model that cat does not know,
// such as the alias "opus", gives the spec of the parent. Any other
// error of cat is an error, such as a bare ID that two providers serve.
// The child then does not run on a model that its file did not name.
func childSpec(cat *catalog.Catalog, parent string, sub def.Agent) (string, error) {
	if sub.InheritsModel() {
		return parent, nil
	}
	file := sub.Model
	_, err := cat.Model(file)
	if errors.Is(err, catalog.ErrUnknownModel) {
		return parent, nil
	}
	if err != nil {
		return "", err
	}
	return file, nil
}

// keepTools returns the tools that sub can call: the tools of its tools
// key, or all of them if it has none, without the tools of its
// disallowedTools key.
func keepTools(tools []tool.Tool, sub def.Agent) []tool.Tool {
	allowed := names(tool.ListNames(sub.Tools))
	denied := names(ruleNames(sub.Disallowed))
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

// ruleNames returns the tool names that the rules of a
// disallowedTools list cover, as [tool.RuleNames] gives them. A rule
// with content, such as "Bash(git push *)", removes the whole tool.
func ruleNames(rules []string) []string {
	var out []string
	for _, r := range rules {
		name, _ := tool.SplitRule(r)
		out = append(out, tool.RuleNames(name)...)
	}
	return out
}

// names returns a set of the names.
func names(list []string) map[string]bool {
	out := make(map[string]bool, len(list))
	for _, n := range list {
		out[n] = true
	}
	return out
}
