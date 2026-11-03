//go:build unix

package cliproc

import (
	"os/exec"
	"syscall"
)

// ownGroup starts cmd in a session of its own, so the CLI leads a new
// process group. A command that a tool of the CLI starts, such as a
// sleep in the background, joins the group, and [signalGroup] reaches
// it. A new session has no terminal, so a command that opens /dev/tty
// gets an error and does not stop.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// signalGroup sends sig to each process in the group of cmd.
func signalGroup(cmd *exec.Cmd, sig syscall.Signal) error {
	return syscall.Kill(-cmd.Process.Pid, sig)
}
