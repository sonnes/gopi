package claude

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"unicode"
	"unicode/utf8"

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
	c := newConfig(d.opts)
	env := environ(c.env)

	out, stderr, err := cliproc.Command(ctx, c.binary, env, "--version")
	if err != nil {
		return agent.Status{}, checkErr(err, stderr)
	}
	status := agent.Status{Version: cliproc.Version(out)}

	out, stderr, err = cliproc.Command(ctx, c.binary, env, "auth", "status", "--json")
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		return status, checkErr(err, stderr)
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
		status.Plan = planName(auth.SubscriptionType)
	case "api_key", "api_key_helper":
		status.Method = agent.AuthAPIKey
	case "third_party":
		status.Method = agent.AuthCloud
		status.Plan = cmp.Or(clouds[auth.APIProvider], auth.APIProvider)
	}
	return status, nil
}

// planName returns the name of a subscription for a person, such as
// "Claude Max" for "max". It returns "" for no subscription.
func planName(subscription string) string {
	if subscription == "" {
		return ""
	}
	first, size := utf8.DecodeRuneInString(subscription)
	return "Claude " + string(unicode.ToUpper(first)) + subscription[size:]
}

// checkErr adds the end of stderr to the error of a check command.
func checkErr(err error, stderr []byte) error {
	reason := strings.TrimSpace(string(stderr))
	if reason == "" {
		return fmt.Errorf("claude: %w", err)
	}
	return fmt.Errorf("claude: %w: %s", err, reason)
}
