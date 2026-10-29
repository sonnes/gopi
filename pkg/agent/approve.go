package agent

import (
	"context"
	"encoding/json"

	"github.com/sonnes/gopi/pkg/ai"
)

// ApproveFunc decides a tool call that needs a person. Each agent of
// gopi takes one through its Approve option: the harness, the Claude
// Code agent, and the Codex agent. One function then answers the
// approvals of each agent.
//
// The request has the call, with the tool name of the agent. Claude
// Code names its tools Bash and Read, and the harness and Codex name
// them bash and read.
//
// The function runs while the agent waits, so it must return when ctx
// ends. If it returns an error, the call does not run.
type ApproveFunc func(ctx context.Context, req ApprovalRequest) (Decision, error)

// Decision answers one tool call. The zero value denies the call.
//
// An agent that cannot honor a field says so in the doc of its Approve
// option. If it cannot honor a field of an allowed call, it denies the
// call.
type Decision struct {
	// Allow lets the call run.
	Allow bool `json:"allow,omitempty"`
	// Always also allows the later calls that match this call, for the
	// life of the agent. The agent decides what matches.
	Always bool `json:"always,omitempty"`
	// Input replaces the input of an allowed call. Nil keeps the input.
	Input json.RawMessage `json:"input,omitempty"`
	// Message tells the model why the call is denied. If it is empty,
	// the agent gives a default reason.
	Message string `json:"message,omitempty"`
}

// UnmarshalJSON decodes a decision. A null input is no input, so a
// client that writes each field gets the input of the call and not a
// null input.
func (d *Decision) UnmarshalJSON(data []byte) error {
	type plain Decision
	var p plain
	err := json.Unmarshal(data, &p)
	if err != nil {
		return err
	}
	if string(p.Input) == "null" {
		p.Input = nil
	}
	*d = Decision(p)
	return nil
}

// ApprovalRequest is a tool call that needs a person, with what the
// agent says about it. A [Decision] answers it.
type ApprovalRequest struct {
	// Call is the tool call.
	Call ai.ToolCall
	// Reason tells a person why the call asks, such as the safety check
	// that the call did not pass. It is empty if the agent gives no
	// reason.
	Reason string
	// NoAlways reports that the agent does not keep an Always decision
	// for this call. An approve function does not offer Always then.
	// The Claude Code agent sets it when the CLI offers no "allow
	// always" for the call, because the rule that "allow always" writes
	// allows more than the call. The agent then allows only this call.
	NoAlways bool
}
