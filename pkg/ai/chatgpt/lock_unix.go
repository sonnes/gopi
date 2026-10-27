//go:build unix

package chatgpt

import (
	"os"
	"path/filepath"
	"syscall"
)

// lockFile takes an exclusive flock on the file at path, and creates the
// file if it does not exist. It waits until the lock is free. The
// returned function releases the lock.
func lockFile(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), dirMode); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, fileMode)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
