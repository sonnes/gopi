package claude

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/internal/cliproc"
)

// authStatus is the part of the output of `claude auth status --json`
// that a check reads.
type authStatus struct {
	LoggedIn bool `json:"loggedIn"`
	// AuthMethod is "claude.ai", "oauth_token", "api_key",
	// "api_key_helper", "third_party", or "none".
	AuthMethod string `json:"authMethod"`
	// APIProvider is "firstParty", or the cloud of a "third_party"
	// method, such as "bedrock".
	APIProvider      string `json:"apiProvider"`
	Email            string `json:"email"`
	OrgName          string `json:"orgName"`
	SubscriptionType string `json:"subscriptionType"`
}

// planBrand starts the name of a subscription, such as "Claude Max".
const planBrand = "Claude"

// clouds names the clouds that Claude Code signs in to.
var clouds = map[string]string{
	"bedrock": "Amazon Bedrock",
	"vertex":  "Google Vertex AI",
	"foundry": "Microsoft Foundry",
}

// Check runs `claude --version` and `claude auth status --json`, with
// the binary and the environment of the driver. The status command
// exits with an error when no account is signed in, and still prints
// its JSON, so a check reads the JSON and not the exit code.
func (d driver) Check(ctx context.Context) (agent.Status, error) {
	c := newOptions(d.opts)
	env := cliproc.Environ(dropped, c.env)

	out, stderr, err := cliproc.Command(ctx, c.binary, env, "--version")
	if err != nil {
		return agent.Status{}, cliproc.CheckErr(cliName, err, stderr)
	}
	status := agent.Status{Version: cliproc.Version(out)}

	out, stderr, err = cliproc.Command(ctx, c.binary, env, "auth", "status", "--json")
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		return status, cliproc.CheckErr(cliName, err, stderr)
	}
	var auth authStatus
	if jsonErr := json.Unmarshal(out, &auth); jsonErr != nil {
		return status, fmt.Errorf("claude: read the auth status: %w", jsonErr)
	}
	if !auth.LoggedIn {
		return status, nil
	}
	status.LoggedIn = true
	status.Account = auth.Email
	status.Org = auth.OrgName
	switch auth.AuthMethod {
	case "claude.ai", "oauth_token":
		status.Method = agent.AuthSubscription
		status.Plan = cliproc.PlanName(planBrand, auth.SubscriptionType)
	case "api_key", "api_key_helper":
		status.Method = agent.AuthAPIKey
	case "third_party":
		status.Method = agent.AuthCloud
		status.Plan = cmp.Or(clouds[auth.APIProvider], auth.APIProvider)
	}
	return status, nil
}
