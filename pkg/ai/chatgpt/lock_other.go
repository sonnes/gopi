//go:build !unix

package chatgpt

import "sync"

// fileLocks holds one lock for each path in this process.
var fileLocks sync.Map

// lockFile locks path for this process only. This system has no flock,
// so two processes on one login file can refresh at the same time, and
// one of them then gets [ErrSignInExpired].
func lockFile(path string) (func(), error) {
	v, _ := fileLocks.LoadOrStore(path, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock, nil
}
