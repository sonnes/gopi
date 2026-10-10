package pathlock_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/sonnes/gopi/pkg/sandbox/internal/pathlock"
)

// wait is how long a test waits to see that a lock blocks.
const wait = 50 * time.Millisecond

// dir is the working directory of the sandbox in the tests.
const dir = "/work"

// locked returns a channel that gets the unlock function when Lock
// returns for path.
func locked(path string) <-chan func() {
	ch := make(chan func(), 1)
	go func() { ch <- pathlock.Lock(dir, path) }()
	return ch
}

func TestLockBlocksTheSamePath(t *testing.T) {
	tests := []struct {
		name   string
		first  string
		second string
	}{
		{name: "a path with a dot", first: "sub/file.txt", second: "sub/./file.txt"},
		{name: "a relative and an absolute path", first: "file.txt", second: "/work/file.txt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			unlock := pathlock.Lock(dir, tt.first)

			second := locked(tt.second)
			select {
			case <-second:
				t.Fatal("a second lock of one path does not wait")
			case <-time.After(wait):
			}

			unlock()
			select {
			case u := <-second:
				u()
			case <-time.After(time.Second):
				t.Fatal("the second lock does not get the path after an unlock")
			}
		})
	}
}

func TestLockDoesNotBlockAnotherPath(t *testing.T) {
	unlock := pathlock.Lock(dir, "a.txt")
	defer unlock()

	select {
	case u := <-locked("b.txt"):
		u()
	case <-time.After(time.Second):
		assert.Fail(t, "a lock of another path waits")
	}
}
