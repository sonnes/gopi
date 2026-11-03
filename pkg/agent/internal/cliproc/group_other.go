//go:build !unix

package cliproc

import (
	"os/exec"
	"syscall"
)

// ownGroup does nothing on a system with no process groups.
func ownGroup(*exec.Cmd) {}

// signalGroup sends sig to the process of cmd only. A system with no
// process groups cannot reach the children of the process.
func signalGroup(cmd *exec.Cmd, sig syscall.Signal) error {
	if sig == syscall.SIGKILL {
		return cmd.Process.Kill()
	}
	return cmd.Process.Signal(sig)
}
