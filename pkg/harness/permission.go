package harness

import (
	"context"
	"fmt"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/permission"
	"github.com/sonnes/gopi/pkg/tool"
	"github.com/sonnes/gopi/pkg/tool/askquestion"
)

// policyHook returns a BeforeTool hook that checks each tool call with
// policy, before the call runs. It asks approve about each call that the
// policy asks about. If approve is nil, each such call is denied.
//
// With askApprover, the ask_user_question tool is the tool of
// [AskTool]. The hook then asks approve about each call of it that no
// Deny rule matches, as a safety check, because only a person answers
// it. It first removes the answers of the model from the input, so only
// the decision of approve can give them.
func policyHook(policy permission.Policy, approve agent.ApproveFunc, askApprover bool) agent.Hook {
	return agent.BeforeTool(func(ctx context.Context, _ agent.State, c ai.ToolCall) (ai.ToolCall, error) {
		d := policy.Decide(c)
		askPerson := askApprover &&
			c.Name == tool.AskUserQuestion &&
			d.Match != permission.DenyMatch
		if askPerson {
			return askQuestion(ctx, policy, approve, c)
		}
		switch d.Match {
		case permission.DenyMatch:
			return c, agent.Deny(d.Reason)
		case permission.AskMatch:
			return requestApproval(ctx, policy, approve, c, d.Safety)
		}
		return c, nil
	})
}

// askQuestion sends the ask_user_question call c to approve as a safety
// check, without the answers of the model. An input that is not a JSON
// object goes as it is, and the tool rejects it.
func askQuestion(
	ctx context.Context,
	policy permission.Policy,
	approve agent.ApproveFunc,
	c ai.ToolCall,
) (ai.ToolCall, error) {
	if input, err := askquestion.WithoutAnswers(c.Input); err == nil {
		c.Input = input
	}
	return requestApproval(ctx, policy, approve, c, true)
}

// requestApproval gets a decision on c from approve. safety marks the
// request as a safety check. If the decision edits the input, the Deny
// rules of policy check the edited input, and the edited input runs.
func requestApproval(
	ctx context.Context,
	policy permission.Policy,
	approve agent.ApproveFunc,
	c ai.ToolCall,
	safety bool,
) (ai.ToolCall, error) {
	if approve == nil {
		reason := fmt.Sprintf(
			"the tool call %q needs approval, and the policy has no approver",
			c.Name,
		)
		return c, agent.Deny(reason)
	}
	req := agent.ApprovalRequest{
		Call:   c,
		Safety: safety,
	}
	d, err := approve(ctx, req)
	if err != nil {
		return c, fmt.Errorf("harness: approve the tool call %q: %w", c.Name, err)
	}
	if !d.Allow {
		reason := d.Message
		if reason == "" {
			reason = fmt.Sprintf("the approver denied the tool call %q", c.Name)
		}
		return c, agent.Deny(reason)
	}
	if d.Input == nil {
		return c, nil
	}
	c.Input = d.Input
	return c, checkEdited(policy, c)
}

// checkEdited returns an [agent.DenyError] if a Deny rule of policy
// matches the call c with the edited input.
func checkEdited(policy permission.Policy, c ai.ToolCall) error {
	deny := permission.Rules{Deny: policy.Rules.Deny}
	match, rule, err := deny.Check(c, policy.Dir)
	if err != nil {
		return agent.Deny(err.Error())
	}
	if match == permission.DenyMatch {
		return agent.Deny(fmt.Sprintf("the rule %q denies the edited tool call %q", rule, c.Name))
	}
	return nil
}
