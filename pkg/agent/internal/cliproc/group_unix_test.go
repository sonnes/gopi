//go:build unix

package cliproc

import (
	"bufio"
	"context"
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

func TestStopIsCleanWhenTheCLIFailsAfterItsInputEnds(t *testing.T) {
	// The Claude Code CLI exits with status 1 when its input ends.
	p, _ := startChild(t, "cat; exit 1")

	require.NoError(t, p.Stop())
}

func TestStopIsCleanWhenTheCLIEndsAtTheSignal(t *testing.T) {
	// The CLI does not read stdin, so only SIGTERM ends it.
	p, _ := startChild(t, "exec sleep 60")

	require.NoError(t, p.Stop())
}

func TestStopReturnsTheExitOfACLIThatFailedBeforeTheStop(t *testing.T) {
	pipes, err := Start(exec.Command("sh", "-c", "exit 3"))
	require.NoError(t, err)
	p := Open(pipes)
	for range p.Lines() {
	}

	var exitErr *exec.ExitError
	require.ErrorAs(t, p.Stop(), &exitErr)
	require.Equal(t, 3, exitErr.ExitCode())
}

func TestKillEndsTheChildrenOfTheCLI(t *testing.T) {
	p, pid := startChild(t, "sleep 60")

	p.Kill()

	waitGone(t, pid)
}

func TestLoginCmdEndsTheGroupWhenCtxEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cmd := LoginCmd(ctx, "sh", nil, "-c", "sleep 60 & echo $!; wait")
	out, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	line, err := bufio.NewReader(out).ReadString('\n')
	require.NoError(t, err)
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	require.NoError(t, err)

	cancel()

	require.Error(t, cmd.Wait())
	waitGone(t, pid)
}
