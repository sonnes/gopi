package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"github.com/sonnes/gopi/pkg/sandbox/fence"
)

// Option configures a [Local] sandbox.
type Option func(*local)

// Policy fences the sandbox with a policy. Each command runs inside a
// [fence.Fence] with the policy, and the file tools obey the same file
// rules: they cannot read a hidden path, and they write only where a
// fenced command can write. The shell of each command does not load the
// profile of the user. Without Policy, the sandbox has no limits. For
// the policy of an agent that works in one project directory:
//
//	sandbox.Local(dir, sandbox.Policy(fence.Workspace(dir)...))
func Policy(opts ...fence.Option) Option {
	return func(l *local) { l.policy = opts }
}

// signalExitBase is the exit code of a shell for a command that a
// signal stopped, before the number of the signal is added.
const signalExitBase = 128

// waitDelay is how long Exec waits for the output pipes to close after
// it stops a command. A background child of the command can keep a
// pipe open after the shell ends.
const waitDelay = time.Second

// shell runs each command in a sandbox without a policy. A login shell
// loads the profile of the user, so the command sees the same PATH as a
// terminal.
var shell = []string{"bash", "-lc"}

// fencedShell runs each command in a sandbox with a policy. It does not
// load the profile, because a profile can export a variable that the
// policy keeps out, such as an API key. The command gets PATH from the
// environment of this process.
var fencedShell = []string{"bash", "-c"}

type local struct {
	dir string

	policy []fence.Option
	fence  fence.Fence
	rules  *fence.Rules
}

// Local returns a sandbox on a directory of this host. If dir is
// relative, Local makes it absolute against the current directory.
//
// By default, the sandbox has no limits. Each command runs as the user
// of this process, with the whole environment, and the tools reach
// every path that the user can reach. [Policy] fences it. Local returns
// an error if the fence of the policy cannot run on this host, for
// example on an OS without a fence backend.
//
// Local returns the interface, because a caller uses a local sandbox
// through the tools, as it uses a remote one.
func Local(dir string, opts ...Option) (Sandbox, error) {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}

	l := &local{dir: dir}
	for _, opt := range opts {
		opt(l)
	}
	if l.policy == nil {
		return l, nil
	}

	f, err := fence.New(l.policy...)
	if err != nil {
		return nil, fmt.Errorf("sandbox: %w", err)
	}
	rules := fence.NewRules(l.policy...)
	l.fence = f
	l.rules = &rules
	return l, nil
}

func (l *local) Dir() string {
	return l.dir
}

func (l *local) ReadFile(_ context.Context, path string) ([]byte, error) {
	abs, err := l.HostPath(path)
	if err != nil {
		return nil, err
	}
	f, err := l.open(abs, path, false)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

func (l *local) WriteFile(_ context.Context, path string, data []byte) error {
	abs, err := l.HostPath(path)
	if err != nil {
		return err
	}
	if l.rules != nil && !l.rules.CanWrite(abs) {
		return fmt.Errorf("path is not writable under the sandbox policy: %s", path)
	}

	err = os.MkdirAll(filepath.Dir(abs), 0o755) //nolint:gosec // Standard directory permissions
	if err != nil {
		return err
	}
	f, err := l.open(abs, path, true)
	if err != nil {
		return err
	}
	if err := f.Truncate(0); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func (l *local) Exec(ctx context.Context, cmd Command) (Result, error) {
	dir := l.dir
	if cmd.Dir != "" {
		dir = l.abs(cmd.Dir)
	}

	argv := shell
	if l.fence != nil {
		argv = fencedShell
	}
	c, err := l.command(ctx, dir, append(slices.Clone(argv), cmd.Line))
	if err != nil {
		return Result{}, err
	}
	c.WaitDelay = waitDelay
	killGroup(c)

	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr

	err = c.Run()
	res := Result{
		Stdout: stdout.Bytes(),
		Stderr: stderr.Bytes(),
	}

	if ctx.Err() != nil {
		return res, ctx.Err()
	}

	// A background child of the command, such as a server started with
	// &, keeps the output pipes open after the shell ends. Exec stops
	// waiting for the pipes after waitDelay. The command itself worked.
	if errors.Is(err, exec.ErrWaitDelay) {
		res.ExitCode = c.ProcessState.ExitCode()
		return res, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		res.ExitCode = exitErr.ExitCode()
		// A signal gives -1. A shell reports 128 plus the signal.
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			res.ExitCode = signalExitBase + int(ws.Signal())
		}
		return res, nil
	}
	return res, err
}

// command returns the command that runs argv in dir: inside the fence,
// or without one for a sandbox without a [Policy].
func (l *local) command(ctx context.Context, dir string, argv []string) (*exec.Cmd, error) {
	if l.fence != nil {
		return l.fence.Command(ctx, dir, argv...)
	}
	c := exec.CommandContext(ctx, argv[0], argv[1:]...)
	c.Dir = dir
	return c, nil
}

// abs returns path as an absolute, clean path on this host.
func (l *local) abs(path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(l.dir, path)
}

// HostPath returns path as an absolute path on this host. With a
// [Policy], it returns an error for a path that the policy hides. The
// tools reach HostPath through [host.Pather].
func (l *local) HostPath(path string) (string, error) {
	if path == "" {
		return "", errors.New("path is required")
	}

	abs := l.abs(path)
	if l.rules != nil && !l.rules.CanRead(abs) {
		return "", fmt.Errorf("path is hidden by the sandbox policy: %s", path)
	}
	return abs, nil
}
