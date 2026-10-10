//go:build !unix

package sandbox

import (
	"io/fs"
	"os/exec"
)

// nonBlock is 0 on this platform, which has no FIFO.
const nonBlock = 0

// hardLinks returns 1 on this platform.
func hardLinks(fs.FileInfo) int { return 1 }

// killGroup does nothing on this platform. When the context ends, only
// the shell stops.
func killGroup(*exec.Cmd) {}
