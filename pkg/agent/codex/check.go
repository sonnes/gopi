package codex

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/internal/cliproc"
)

// loggedInPrefix starts the output of `codex login status` when an
// account is signed in, such as "Logged in using ChatGPT".
const loggedInPrefix = "Logged in using "

// Check runs `codex --version` and `codex login status`, with the
// binary and the environment of the driver. The status command writes
// to stderr, and exits with an error when no account is signed in. For
// a ChatGPT account, a check also reads auth.json in the Codex home for
// the plan, the email, and the organization.
func (d driver) Check(ctx context.Context) (agent.Status, error) {
	c := newConfig(d.opts)
	env := append(os.Environ(), c.env...)

	out, stderr, err := cliproc.Command(ctx, c.binary, env, "--version")
	if err != nil {
		return agent.Status{}, checkErr(err, stderr)
	}
	status := agent.Status{Version: cliproc.Version(out)}

	out, stderr, err = cliproc.Command(ctx, c.binary, env, "login", "status")
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		return status, checkErr(err, stderr)
	}
	line := strings.TrimSpace(string(stderr) + string(out))
	method, loggedIn := strings.CutPrefix(line, loggedInPrefix)
	if !loggedIn {
		return status, nil
	}
	status.LoggedIn = true
	if strings.Contains(method, "API key") {
		status.Method = agent.AuthAPIKey
		return status, nil
	}
	status.Method = agent.AuthSubscription
	status.Plan = method
	if claims, ok := readClaims(codexHome(env)); ok {
		status.Plan = cmp.Or(claims.plan(), status.Plan)
		status.Account = claims.Email
		status.Org = claims.org()
	}
	return status, nil
}

// codexHome returns the Codex home of env: CODEX_HOME, or ~/.codex. It
// returns "" if neither is known.
func codexHome(env []string) string {
	for _, v := range slices.Backward(env) {
		if home, ok := strings.CutPrefix(v, "CODEX_HOME="); ok && home != "" {
			return home
		}
	}
	dir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, ".codex")
}

// authFile is the part of auth.json in the Codex home that a check
// reads. The file also holds the tokens and the key, and a check reads
// only the claims of the ID token.
type authFile struct {
	Tokens struct {
		IDToken string `json:"id_token"`
	} `json:"tokens"`
}

// idClaims are the claims of the ID token of a ChatGPT account.
type idClaims struct {
	Email string `json:"email"`
	Auth  struct {
		PlanType      string `json:"chatgpt_plan_type"`
		Organizations []struct {
			Title     string `json:"title"`
			IsDefault bool   `json:"is_default"`
		} `json:"organizations"`
	} `json:"https://api.openai.com/auth"`
}

// readClaims reads the claims of the ID token in auth.json of home. It
// does not verify the token, because the check only shows the claims.
// The file is a private format of Codex, so readClaims reports false
// for a file that it cannot read, and the check keeps the status line.
func readClaims(home string) (idClaims, bool) {
	if home == "" {
		return idClaims{}, false
	}
	data, err := os.ReadFile(filepath.Join(home, "auth.json"))
	if err != nil {
		return idClaims{}, false
	}
	var file authFile
	if json.Unmarshal(data, &file) != nil {
		return idClaims{}, false
	}
	parts := strings.Split(file.Tokens.IDToken, ".")
	if len(parts) != 3 {
		return idClaims{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return idClaims{}, false
	}
	var claims idClaims
	if json.Unmarshal(payload, &claims) != nil {
		return idClaims{}, false
	}
	return claims, true
}

// plan returns the name of the ChatGPT plan, such as "ChatGPT Plus" for
// "plus". It returns "" for no plan.
func (c idClaims) plan() string {
	t := c.Auth.PlanType
	if t == "" {
		return ""
	}
	first, size := utf8.DecodeRuneInString(t)
	return "ChatGPT " + string(unicode.ToUpper(first)) + t[size:]
}

// org returns the title of the default organization, or "".
func (c idClaims) org() string {
	for _, o := range c.Auth.Organizations {
		if o.IsDefault {
			return o.Title
		}
	}
	return ""
}

// checkErr adds the end of stderr to the error of a check command.
func checkErr(err error, stderr []byte) error {
	reason := strings.TrimSpace(string(stderr))
	if reason == "" {
		return fmt.Errorf("codex: %w", err)
	}
	return fmt.Errorf("codex: %w: %s", err, reason)
}
