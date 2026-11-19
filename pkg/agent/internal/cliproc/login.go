package cliproc

import (
	"context"
	"os/exec"
	"syscall"
	"time"
)

// loginGrace is how long a sign-in has to exit after SIGTERM, before
// it gets SIGKILL.
const loginGrace = 3 * time.Second

// LoginCmd returns the command of a sign-in: binary with args and the
// environment env, not started. The CLI leads a process group of its
// own. When ctx ends, the command sends SIGTERM to the whole group, and
// SIGKILL to the CLI after [loginGrace].
func LoginCmd(ctx context.Context, binary string, env []string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = env
	ownGroup(cmd)
	cmd.Cancel = func() error { return signalGroup(cmd, syscall.SIGTERM) }
	cmd.WaitDelay = loginGrace
	return cmd
}
