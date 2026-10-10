// Package pathlock lets one change to a file run at a time. The edit
// tool reads a file, changes it, and writes it back. Two edits of one
// file at the same time lose one of the changes, so the edit tool and
// the write tool hold the lock of the path while they run.
//
// The lock is for the absolute path, not the sandbox. A relative path
// and an absolute path of one file share a lock. Two sandboxes with one
// directory also share it, which costs time and loses no change.
package pathlock

import (
	"path"
	"sync"
)

var (
	mu    sync.Mutex
	locks = map[string]*lock{}
)

// lock is the mutex of one path, with a count of its holders and
// waiters. The last one removes it, so the map does not grow.
type lock struct {
	mu sync.Mutex
	n  int
}

// Lock waits until no other caller holds the path p, and returns the
// function that releases it. dir is the working directory of the
// sandbox. Lock makes a relative p absolute against dir and cleans it,
// so file, ./file, and dir/file share a lock.
func Lock(dir, p string) (unlock func()) {
	key := path.Clean(p)
	if !path.IsAbs(key) {
		key = path.Join(dir, key)
	}

	mu.Lock()
	l, ok := locks[key]
	if !ok {
		l = &lock{}
		locks[key] = l
	}
	l.n++
	mu.Unlock()

	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		mu.Lock()
		l.n--
		if l.n == 0 {
			delete(locks, key)
		}
		mu.Unlock()
	}
}
