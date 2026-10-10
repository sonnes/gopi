//go:build unix

package sandbox

import (
	"io/fs"
	"os/exec"
	"syscall"
)

// nonBlock opens a file without a wait, so that the open of a FIFO
// does not block.
const nonBlock = syscall.O_NONBLOCK

// hardLinks returns the number of hard links of a file.
func hardLinks(info fs.FileInfo) int {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 1
	}
	return int(st.Nlink)
}

// killGroup starts the command in its own session, and stops the whole
// process group when the context ends. If only the shell stops, a child
// such as a sleep or a server keeps running after the timeout.
//
// A new session has no terminal. A command that opens /dev/tty to ask
// for a password, such as ssh or git, gets an error. In a process group
// of the terminal that is not in front, the system stops such a command,
// and it waits until the timeout.
func killGroup(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	c.Cancel = func() error {
		return syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
	}
}
