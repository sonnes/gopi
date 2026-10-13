package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/sonnes/gopi/pkg/agent/internal/cliproc"
)

// start starts cmd. Tests replace it to replay a trace instead.
var start = cliproc.Start

// process is one running CLI. The reader goroutine of the [cliproc.Proc]
// owns stdout. The run that owns the agent reads the lines and writes
// to stdin, so one goroutine at a time does both.
type process struct {
	p *cliproc.Proc
	// nextID numbers the control requests of the agent, so that a trace
	// of the process is the same on every run.
	nextID int
	// canUseTool answers the permission prompts, or is nil.
	canUseTool CanUseToolFunc
}

// launch starts cmd and its reader goroutine. canUseTool answers the
// permission prompts of the CLI. If it is nil, the agent denies them.
func launch(cmd *exec.Cmd, canUseTool CanUseToolFunc) (*process, error) {
	pipes, err := start(cmd)
	if err != nil {
		return nil, fmt.Errorf("claude: start %s: %w", cmd.Path, err)
	}
	pr := &process{
		p:          cliproc.Open(pipes),
		canUseTool: canUseTool,
	}
	return pr, nil
}

// lines returns the lines of stdout. The channel closes when stdout
// ends.
func (pr *process) lines() <-chan []byte { return pr.p.Lines() }

// exited returns the error for a CLI whose stdout ended.
func (pr *process) exited() error {
	if err := pr.p.ReadErr(); err != nil {
		return fmt.Errorf("claude: read the CLI output: %w", err)
	}
	if text := pr.p.Stderr(); text != "" {
		return fmt.Errorf("claude: the CLI exited: %s", text)
	}
	return errors.New("claude: the CLI exited")
}

// send writes v to stdin as one JSON line.
func (pr *process) send(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := pr.p.Write(data); err != nil {
		// A CLI that rejects its flags exits at once, so the first write
		// can fail before stdout ends. Stderr says why.
		if text := pr.p.Stderr(); text != "" {
			return fmt.Errorf("claude: the CLI exited: %s", text)
		}
		return fmt.Errorf("claude: write to the CLI: %w", err)
	}
	return nil
}

// request sends a control request with the next request ID and
// returns the ID.
func (pr *process) request(subtype string) (string, error) {
	pr.nextID++
	id := fmt.Sprintf("req_%d", pr.nextID)
	err := pr.send(outRequest{
		Type:      "control_request",
		RequestID: id,
		Request:   outRequestBody{Subtype: subtype},
	})
	return id, err
}

// answer sends the one response to a control request from the CLI. A
// permission prompt gets the decision of canUseTool, or a denial
// without it. Every other request gets an error.
func (pr *process) answer(ctx context.Context, l *line) error {
	body := outResponseBody{
		Subtype:   "success",
		RequestID: l.RequestID,
	}
	isPrompt := l.Request != nil && l.Request.Subtype == "can_use_tool"
	if isPrompt {
		resp, err := pr.decide(ctx, l.Request)
		if err != nil {
			body.Subtype = "error"
			body.Error = err.Error()
		} else {
			body.Response = resp
		}
	} else {
		subtype := ""
		if l.Request != nil {
			subtype = l.Request.Subtype
		}
		body.Subtype = "error"
		body.Error = fmt.Sprintf("gopi does not support the control request %q", subtype)
	}
	return pr.send(outResponse{
		Type:     "control_response",
		Response: body,
	})
}

// decide returns the answer to the permission prompt req.
func (pr *process) decide(ctx context.Context, req *controlRequest) (permission, error) {
	deny := permission{
		Behavior:  "deny",
		Message:   denyText,
		ToolUseID: req.ToolUseID,
	}
	if pr.canUseTool == nil {
		return deny, nil
	}
	d, err := pr.canUseTool(ctx, ToolRequest{
		ToolUseID:   req.ToolUseID,
		ToolName:    req.ToolName,
		Input:       req.Input,
		Suggestions: req.PermissionSuggestions,
	})
	if err != nil {
		return permission{}, err
	}
	if !d.Allow {
		deny.Message = d.Message
		if deny.Message == "" {
			deny.Message = deniedText
		}
		return deny, nil
	}
	input := d.Input
	if input == nil {
		input = req.Input
	}
	allow := permission{
		Behavior:     "allow",
		UpdatedInput: input,
		ToolUseID:    req.ToolUseID,
	}
	if d.Always {
		allow.UpdatedPermissions = req.PermissionSuggestions
	}
	return allow, nil
}

// denyText is the reason that the agent gives the model when it denies
// a permission prompt with no [CanUseTool] function.
const denyText = "gopi does not answer permission prompts. Set a permission mode that allows the tool."

// deniedText is the reason that the agent gives the model when a
// [CanUseTool] function denies a call with no message.
const deniedText = "The caller denied the tool call."

// initialize sends the initialize request and waits for its response.
// It answers the control requests that arrive first.
func (pr *process) initialize(ctx context.Context) error {
	id, err := pr.request("initialize")
	if err != nil {
		return err
	}
	for {
		var data []byte
		var ok bool
		select {
		case <-ctx.Done():
			return ctx.Err()
		case data, ok = <-pr.lines():
		}
		if !ok {
			return pr.exited()
		}
		var l line
		if err := json.Unmarshal(data, &l); err != nil {
			continue
		}
		switch {
		case l.Type == "result":
			// A CLI that cannot start the session reports a result
			// before the response, then exits.
			if lostSession(&l) {
				return ErrNoConversation
			}
			return resultError(&l)
		case l.Type == "control_request":
			if err := pr.answer(ctx, &l); err != nil {
				return err
			}
		case l.Type == "control_response" && l.Response != nil && l.Response.RequestID == id:
			if l.Response.Subtype != "success" {
				return fmt.Errorf("claude: initialize: %s", l.Response.Error)
			}
			return nil
		}
	}
}

// lostSessionText starts the error of a CLI that cannot find the
// session to resume.
const lostSessionText = "No conversation found with session ID"

// lostSession reports whether the result line l says that the CLI
// cannot find the session to resume.
func lostSession(l *line) bool {
	return slices.ContainsFunc(l.Errors, func(s string) bool {
		return strings.HasPrefix(s, lostSessionText)
	})
}

// stop ends the input of the CLI and waits for it to exit. See
// [cliproc.Proc.Stop].
func (pr *process) stop() error { return pr.p.Stop() }

// terminate kills the CLI at once and waits for it to exit.
func (pr *process) terminate() { pr.p.Kill() }

// dropped are the variables of this process that the CLI does not get:
//
//   - CLAUDECODE marks a process inside a Claude Code session. The
//     agent starts a session of its own, so the CLI must not inherit
//     the mark.
//   - NODE_OPTIONS of a Node parent can break the CLI. The official
//     SDK drops it too.
//   - DEBUG turns on the debug output of the CLI. The official SDK
//     drops it too.
var dropped = []string{"CLAUDECODE", "NODE_OPTIONS", "DEBUG"}

// environ returns the environment of the CLI: the environment of this
// process without the [dropped] variables, then extra.
func environ(extra []string) []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if slices.Contains(dropped, name) {
			continue
		}
		env = append(env, kv)
	}
	return append(env, extra...)
}
