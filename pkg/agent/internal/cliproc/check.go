package cliproc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/sonnes/gopi/pkg/agent"
)

// CheckTimeout caps each command of a check. A CLI that waits for a
// login or a network call does not exit, and a check must end.
const CheckTimeout = 10 * time.Second

// Command runs binary with args and the environment env, for a check.
// It returns stdout and stderr. If binary does not exist, the error
// wraps [agent.ErrNotInstalled]. If the command exits with an error, the
// error is an [*exec.ExitError], and the output is still returned, since
// a CLI can report a state with its exit code.
func Command(ctx context.Context, binary string, env []string, args ...string) ([]byte, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, CheckTimeout)
	defer cancel()
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = env
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	notInstalled := errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist)
	if notInstalled {
		return nil, nil, fmt.Errorf("%w: %s: %w", agent.ErrNotInstalled, binary, err)
	}
	return stdout.Bytes(), stderr.Bytes(), err
}

// Version returns the first word of out that starts with a digit, such
// as "2.1.308" in "2.1.308 (Claude Code)" or "0.163.0" in
// "codex-cli 0.163.0". It returns "" if no word starts with a digit.
func Version(out []byte) string {
	for word := range strings.FieldsSeq(string(out)) {
		if unicode.IsDigit(rune(word[0])) {
			return word
		}
	}
	return ""
}

// CheckErr returns the error of a check command of the CLI named cli,
// with the end of its stderr.
func CheckErr(cli string, err error, stderr []byte) error {
	reason := strings.TrimSpace(string(stderr))
	if reason == "" {
		return fmt.Errorf("%s: %w", cli, err)
	}
	return fmt.Errorf("%s: %w: %s", cli, err, reason)
}

// PlanName returns the name of a plan for a person, such as
// "Claude Max" for the brand "Claude" and the plan "max". It returns ""
// for no plan.
func PlanName(brand, plan string) string {
	if plan == "" {
		return ""
	}
	first, size := utf8.DecodeRuneInString(plan)
	return brand + " " + string(unicode.ToUpper(first)) + plan[size:]
}
