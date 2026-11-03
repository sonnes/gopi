//go:build unix

package cliproc

import (
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// startChild starts a shell that starts a sleep in the background,
// writes the PID of the sleep, and then runs script. It returns the
// process and the PID of the sleep.
func startChild(t *testing.T, script string) (*Proc, int) {
	t.Helper()
	cmd := exec.Command("sh", "-c", "sleep 60 & echo $!; "+script)
	pipes, err := Start(cmd)
	require.NoError(t, err)
	p := Open(pipes)
	line, ok := <-p.Lines()
	require.True(t, ok, "the shell writes the PID of the sleep")
	pid, err := strconv.Atoi(strings.TrimSpace(string(line)))
	require.NoError(t, err)
	return p, pid
}

// waitGone waits until no process has the PID pid.
func waitGone(t *testing.T, pid int) {
	t.Helper()
	require.Eventually(t, func() bool {
		return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
	}, 5*time.Second, 10*time.Millisecond, "the child %d still runs", pid)
}

func TestStopEndsTheChildrenOfTheCLI(t *testing.T) {
	// cat exits when stdin closes, so the shell exits at once.
	p, pid := startChild(t, "cat")

	p.Stop()

	waitGone(t, pid)
}

func TestKillEndsTheChildrenOfTheCLI(t *testing.T) {
	p, pid := startChild(t, "sleep 60")

	p.Kill()

	waitGone(t, pid)
}
