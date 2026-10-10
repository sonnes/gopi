package sandbox

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// open opens the host file abs for a file tool. path is the path of the
// model, for the errors.
//
// open refuses a file that is not a regular file, such as a FIFO, a
// device, or a directory: a read of a FIFO blocks, and /dev/zero never
// ends. The open does not block on a FIFO.
//
// With a [Policy], open checks the path of the open file again, as the
// kernel knows it. A fenced command can change a symlink after the
// check of [local.HostPath] and before the open, so only the second
// check proves where the tool reads or writes. For a write, open also
// refuses a file with more than one hard link, because the other link
// can be outside the write directories. If the check fails after open
// created the file, open removes it.
func (l *local) open(abs, path string, write bool) (*os.File, error) {
	f, created, err := l.openFile(abs, write)
	if err != nil {
		return nil, err
	}

	real, err := l.check(f, path, write)
	if err != nil {
		_ = f.Close()
		if created && real != "" {
			_ = os.Remove(real)
		}
		return nil, err
	}
	return f, nil
}

// openFile opens abs. For a write with a [Policy], it creates the file
// only if it does not exist, and it reports if it created it. A path
// that is a symlink to a file that does not exist is then an error.
func (l *local) openFile(abs string, write bool) (*os.File, bool, error) {
	if !write {
		f, err := os.OpenFile(abs, os.O_RDONLY|nonBlock, 0)
		return f, false, err
	}
	if l.rules == nil {
		f, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|nonBlock, 0o644) //nolint:gosec // Standard file permissions
		return f, false, err
	}

	f, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL|nonBlock, 0o644) //nolint:gosec // Standard file permissions
	if err == nil {
		return f, true, nil
	}
	if !errors.Is(err, fs.ErrExist) {
		return nil, false, err
	}
	f, err = os.OpenFile(abs, os.O_WRONLY|nonBlock, 0)
	return f, false, err
}

// check returns an error for an open file that a tool must not use. It
// returns the path of the file as the kernel knows it, if the sandbox
// has a [Policy] and the path is known.
func (l *local) check(f *os.File, path string, write bool) (string, error) {
	var real string
	if l.rules != nil {
		p, err := getPath(f)
		if err != nil {
			return "", err
		}
		real = p
	}

	info, err := f.Stat()
	if err != nil {
		return real, err
	}
	if !info.Mode().IsRegular() {
		return real, fmt.Errorf("%s is not a regular file", path)
	}
	if l.rules == nil {
		return real, nil
	}

	if !write {
		if !l.rules.CanRead(real) {
			return real, fmt.Errorf("path is hidden by the sandbox policy: %s", path)
		}
		return real, nil
	}
	if !l.rules.CanWrite(real) {
		return real, fmt.Errorf("path is not writable under the sandbox policy: %s", path)
	}
	if hardLinks(info) > 1 {
		return real, fmt.Errorf("path has more than one hard link: %s", path)
	}
	return real, nil
}
