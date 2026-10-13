// Package cliproc starts, reads, and stops the process of an agent CLI.
// Each CLI agent writes JSON lines to stdin and reads JSON lines from
// stdout. A [Proc] owns the process: one goroutine reads stdout into
// [Proc.Lines], and [Proc.Stop] ends the process in steps, so that the
// CLI can write its session file.
package cliproc

import (
	"bufio"
	"io"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/sonnes/gopi/pkg/agent/internal/tail"
)

// maxLine is the longest line that a Proc reads from stdout. A tool
// result or a system line can be large.
const maxLine = 64 << 20

// exitGrace is how long [Proc.Stop] waits for the CLI to exit after
// its input ends, before it sends SIGTERM.
const exitGrace = 2 * time.Second

// killTimeout is how long [Proc.Stop] waits for the CLI to exit after
// SIGTERM, before it kills the process.
const killTimeout = 5 * time.Second

// stderrMax is the number of bytes of stderr that a Proc keeps for the
// error of a CLI that exits.
const stderrMax = 2 << 10

// stderrGrace is how long a Proc waits for stderr to end after stdout
// ends.
const stderrGrace = time.Second

// Pipes is a started process as a CLI agent sees it. Tests build one
// that replays a trace.
type Pipes struct {
	Stdin  io.WriteCloser
	Stdout io.ReadCloser
	Wait   func() error
	// Term asks the process to stop, or is nil if it cannot.
	Term func() error
	Kill func() error
	// Stderr returns the last text of stderr, or is nil for no stderr.
	Stderr func() string
}

// Start starts cmd with pipes on its stdin, stdout, and stderr. It
// keeps the end of stderr for the error of a CLI that exits, such as
// one that rejects a flag. It does not write stderr to the terminal,
// which a TUI owns.
func Start(cmd *exec.Cmd) (Pipes, error) {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return Pipes{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Pipes{}, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return Pipes{}, err
	}
	if err := cmd.Start(); err != nil {
		return Pipes{}, err
	}
	errTail := tail.Read(stderr, stderrMax)
	return Pipes{
		Stdin:  stdin,
		Stdout: stdout,
		Wait:   cmd.Wait,
		Term:   func() error { return cmd.Process.Signal(syscall.SIGTERM) },
		Kill:   cmd.Process.Kill,
		Stderr: func() string { return errTail.Text(stderrGrace) },
	}, nil
}

// Proc is one running CLI. One reader goroutine owns stdout and sends
// each line to [Proc.Lines].
type Proc struct {
	pipes Pipes
	lines chan []byte
	// readErr is the error that ended stdout. It is set before lines is
	// closed.
	readErr error

	stopOnce sync.Once
	stopErr  error
}

// Open starts the reader of p.
func Open(p Pipes) *Proc {
	pr := &Proc{
		pipes: p,
		lines: make(chan []byte),
	}
	go pr.read()
	return pr
}

// read sends each line of stdout to lines until stdout ends. After a
// read error, read discards the rest of stdout, so that the CLI does
// not block on a full pipe.
func (pr *Proc) read() {
	sc := bufio.NewScanner(pr.pipes.Stdout)
	sc.Buffer(nil, maxLine)
	for sc.Scan() {
		pr.lines <- append([]byte(nil), sc.Bytes()...)
	}
	pr.readErr = sc.Err()
	close(pr.lines)
	io.Copy(io.Discard, pr.pipes.Stdout)
}

// Lines returns the lines of stdout. The channel closes when stdout
// ends.
func (pr *Proc) Lines() <-chan []byte { return pr.lines }

// ReadErr returns the error that ended stdout, or nil if stdout ended
// at EOF. Call it only after Lines closes.
func (pr *Proc) ReadErr() error { return pr.readErr }

// Stderr returns the end of the stderr of the CLI, or "" if there is
// none.
func (pr *Proc) Stderr() string {
	if pr.pipes.Stderr == nil {
		return ""
	}
	return pr.pipes.Stderr()
}

// Write writes data to stdin.
func (pr *Proc) Write(data []byte) error {
	_, err := pr.pipes.Stdin.Write(data)
	return err
}

// Stop ends the input of the CLI and waits for it to exit. Stop reads
// the remaining lines, so that the CLI does not block on a full pipe.
// If the CLI does not exit in [exitGrace], Stop sends SIGTERM, so that
// the CLI can write its session file. If it does not exit in
// [killTimeout] after that, Stop kills it.
func (pr *Proc) Stop() error {
	pr.stopOnce.Do(func() {
		pr.pipes.Stdin.Close()
		go pr.drain()
		done := make(chan error, 1)
		go func() { done <- pr.pipes.Wait() }()
		select {
		case pr.stopErr = <-done:
			return
		case <-time.After(exitGrace):
		}
		if pr.pipes.Term != nil {
			pr.pipes.Term()
		}
		select {
		case pr.stopErr = <-done:
		case <-time.After(killTimeout):
			pr.pipes.Kill()
			pr.stopErr = <-done
		}
	})
	return pr.stopErr
}

// Kill kills the CLI at once and waits for it to exit.
func (pr *Proc) Kill() {
	pr.stopOnce.Do(func() {
		pr.pipes.Stdin.Close()
		pr.pipes.Kill()
		go pr.drain()
		pr.pipes.Wait()
	})
}

// drain reads the lines that nobody reads, until stdout ends.
func (pr *Proc) drain() {
	for range pr.lines {
	}
}
