// Package bash is the bash tool. The tool runs a shell command in a
// sandbox.
package bash

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sonnes/gopi/pkg/sandbox"
	"github.com/sonnes/gopi/pkg/tool"
)

const (
	// maxLines is the largest number of lines of output that the tool
	// returns. It keeps the last lines, because the error of a failed
	// command is usually at the end.
	maxLines = 2000
	// maxBytes is the largest number of bytes of output that the tool
	// returns.
	maxBytes = 50 * 1024
	// defaultTimeout is the timeout of a command when the model gives
	// no timeout. A command with no timeout that waits for input stops
	// the agent until a person ends the process.
	defaultTimeout = 300 * time.Second
	// maxTimeout is the longest timeout that the model can give.
	maxTimeout = time.Hour
)

// nonInteractive sets the environment variables that stop a command from
// waiting for input that the model cannot type: a prompt for a password,
// an editor, or a pager. The line runs after the shell loads a profile
// of the user, so a profile cannot change these variables back.
const nonInteractive = "export GIT_TERMINAL_PROMPT=0 GIT_EDITOR=true EDITOR=true VISUAL=true PAGER=cat GIT_PAGER=cat; "

// bashInput is the input of the bash tool.
type bashInput struct {
	Command string `json:"command" jsonschema:"command to run"`
	Timeout *int   `json:"timeout,omitempty" jsonschema:"timeout in seconds, default 300, max 3600"`
}

// timeout returns the timeout that the model gave, or defaultTimeout,
// and at most maxTimeout. It limits the seconds before it makes them a
// duration, because a large number of seconds overflows a duration.
func (in bashInput) timeout() time.Duration {
	if in.Timeout == nil || *in.Timeout <= 0 {
		return defaultTimeout
	}
	seconds := min(*in.Timeout, int(maxTimeout/time.Second))
	return time.Duration(seconds) * time.Second
}

// Name is the name of the tool.
const Name = "bash"

// description is the text that the model reads for the tool.
//
//go:embed description.md
var description string

// New returns the bash tool on box. The tool runs a command in the
// directory of the sandbox and returns stdout, then stderr. It keeps the
// last maxLines lines and maxBytes bytes, and writes the full output to
// a file in [sandbox.ScratchDir]. A command that ends with a nonzero
// exit code is an error result.
//
// Each command starts in the directory of the sandbox, so a cd does not
// carry over to the next call. Every sandbox starts a new shell for each
// command, and a saved directory needs a sentinel line in the output of
// each command. The model joins dependent commands with && in one call.
func New(box sandbox.Sandbox) tool.Tool {
	return tool.New(Name, func(ctx context.Context, in bashInput) (string, error) {
		return run(ctx, box, in)
	}, tool.Description(description))
}

func run(ctx context.Context, box sandbox.Sandbox, in bashInput) (string, error) {
	if in.Command == "" {
		return "", errors.New("command is required")
	}

	timeout := in.timeout()
	parent := ctx
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	res, err := box.Exec(ctx, sandbox.Command{Line: nonInteractive + in.Command})
	full := strings.TrimRight(joinOutput(res.Stdout, res.Stderr), "\n")
	out := tail(parent, box, full)

	// A deadline of the caller also ends ctx. Only the timeout of the
	// tool is a timeout of the command.
	if errors.Is(err, context.DeadlineExceeded) && parent.Err() == nil {
		return "", errors.New(withStatus(out, fmt.Sprintf("Command timed out after %d seconds", int(timeout.Seconds()))))
	}
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", errors.New(withStatus(out, fmt.Sprintf("Command exited with code %d", res.ExitCode)))
	}
	if out == "" {
		return "(no output)", nil
	}
	return out, nil
}

// joinOutput returns stdout, then stderr. It adds a newline between them
// when stdout does not end with one, so that the last line of stdout and
// the first line of stderr stay apart.
func joinOutput(stdout, stderr []byte) string {
	out := string(stdout)
	if out != "" && len(stderr) > 0 && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out + string(stderr)
}

// withStatus adds a status line after the output.
func withStatus(out, status string) string {
	if out == "" {
		return status
	}
	return out + "\n\n" + status
}

// tail returns the last lines of full that fit in maxLines and maxBytes.
// If it cuts full, it writes full to a file in [sandbox.ScratchDir] and
// adds a note with the path of the file.
func tail(ctx context.Context, box sandbox.Sandbox, full string) string {
	lines := strings.Split(full, "\n")
	total := len(lines)

	size := 0
	first := total
	byBytes := false
	for first > 0 && total-first < maxLines {
		next := len(lines[first-1])
		if first < total {
			next++
		}
		if size+next > maxBytes {
			byBytes = true
			break
		}
		size += next
		first--
	}
	if first == 0 && !byBytes {
		return full
	}

	file := path.Join(sandbox.ScratchDir(box), fmt.Sprintf("bash-%d.log", time.Now().UnixNano()))
	where := "Full output: " + file
	if err := box.WriteFile(ctx, file, []byte(full)); err != nil {
		where = "The full output is lost: " + err.Error()
	}

	if first == total {
		// The last line alone is over maxBytes. Keep its end.
		last := lines[total-1]
		cut := len(last) - maxBytes
		for cut < len(last) && !utf8.RuneStart(last[cut]) {
			cut++
		}
		return fmt.Sprintf("%s\n\n[Showing the last 50KB of line %d (the line is %d bytes). %s]", last[cut:], total, len(last), where)
	}

	shown := strings.Join(lines[first:], "\n")
	limit := ""
	if byBytes {
		limit = " (50KB limit)"
	}
	return fmt.Sprintf("%s\n\n[Showing lines %d-%d of %d%s. %s]", shown, first+1, total, total, limit, where)
}
