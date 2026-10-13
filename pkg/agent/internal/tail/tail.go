// Package tail keeps the last bytes of a stream, such as the stderr of
// a CLI. A CLI agent reads the stderr of its process with a [Tail], and
// puts the text in the error of a process that exits. The stderr of a
// CLI holds the reason for a failure at start, such as a bad flag, and
// the agent must not write it to the terminal of a TUI.
package tail

import (
	"bytes"
	"io"
	"regexp"
	"sync"
	"time"
)

// ansi matches the terminal color codes of a log line.
var ansi = regexp.MustCompile("\x1b\\[[0-9;]*m")

// Tail reads a stream to its end and keeps its last bytes.
type Tail struct {
	max  int
	done chan struct{}

	mu  sync.Mutex
	buf []byte
}

// Read starts to read r in a goroutine and keeps the last max bytes.
// It reads until r ends or fails.
func Read(r io.Reader, max int) *Tail {
	t := &Tail{
		max:  max,
		done: make(chan struct{}),
	}
	go func() {
		defer close(t.done)
		io.Copy(t, r)
	}()
	return t
}

// Write keeps the last bytes of p. It never fails, so the copy of the
// stream runs to its end.
func (t *Tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
	}
	return len(p), nil
}

// Text waits up to grace for the stream to end, then returns the kept
// text without color codes and without the space at each end. A CLI
// closes stderr when it exits, a short time after it closes stdout, so
// a caller that saw stdout end gives it a grace period.
func (t *Tail) Text(grace time.Duration) string {
	select {
	case <-t.done:
	case <-time.After(grace):
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	text := ansi.ReplaceAll(t.buf, nil)
	return string(bytes.TrimSpace(text))
}
