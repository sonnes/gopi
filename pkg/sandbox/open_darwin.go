package sandbox

import (
	"bytes"
	"os"
	"syscall"
	"unsafe"
)

// maxPathLen is MAXPATHLEN of macOS, the size of the buffer of
// F_GETPATH.
const maxPathLen = 1024

// getPath returns the path of the open file f, as the kernel knows it.
// The path has no symlinks.
func getPath(f *os.File) (string, error) {
	conn, err := f.SyscallConn()
	if err != nil {
		return "", err
	}

	var buf [maxPathLen]byte
	var errno syscall.Errno
	err = conn.Control(func(fd uintptr) {
		_, _, errno = syscall.Syscall(
			syscall.SYS_FCNTL,
			fd,
			syscall.F_GETPATH,
			uintptr(unsafe.Pointer(&buf[0])),
		)
	})
	if err != nil {
		return "", err
	}
	if errno != 0 {
		return "", &os.PathError{Op: "fcntl F_GETPATH", Path: f.Name(), Err: errno}
	}

	n := bytes.IndexByte(buf[:], 0)
	if n < 0 {
		n = len(buf)
	}
	return string(buf[:n]), nil
}
