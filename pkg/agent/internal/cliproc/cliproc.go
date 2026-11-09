// Package cliproc starts, reads, and stops the process of an agent CLI.
// Each CLI agent writes JSON lines to stdin and reads JSON lines from
// stdout. A [Proc] owns the process: one goroutine reads stdout into
// [Proc.Lines], and [Proc.Stop] ends the process in steps, so that the
// CLI can write its session file. On a Unix system, the CLI leads a
// process group of its own, so a stop or a kill also ends each command
// that the CLI started.
package cliproc

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
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
	// Kill kills the process and each process that it started. [Proc]
	// also calls it after the process exits, to end the processes that
	// it left, so a call after the exit must be safe.
	Kill func() error
	// Stderr returns the last text of stderr, or is nil for no stderr.
	Stderr func() string
}

// Start starts cmd with pipes on its stdin, stdout, and stderr. It
// keeps the end of stderr for the error of a CLI that exits, such as
// one that rejects a flag. It does not write stderr to the terminal,
// which a TUI owns. The process leads a process group, and Term and
// Kill signal the whole group.
func Start(cmd *exec.Cmd) (Pipes, error) {
	ownGroup(cmd)
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
		Term:   func() error { return signalGroup(cmd, syscall.SIGTERM) },
		Kill:   func() error { return signalGroup(cmd, syscall.SIGKILL) },
		Stderr: func() string { return errTail.Text(stderrGrace) },
	}, nil
}

// Proc is one running CLI. One reader goroutine owns stdout and sends
// each line to [Proc.Lines].
type Proc struct {
	pipes Pipes
	lines chan []byte
	// readErr is the error that ended stdout. It is set before readDone
	// and lines close.
	readErr  error
	readDone chan struct{}

	stopOnce sync.Once
	stopErr  error
}

// Open starts the reader of p.
func Open(p Pipes) *Proc {
	pr := &Proc{
		pipes:    p,
		lines:    make(chan []byte),
		readDone: make(chan struct{}),
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
	close(pr.readDone)
	close(pr.lines)
	io.Copy(io.Discard, pr.pipes.Stdout)
}

// Lines returns the lines of stdout. The channel closes when stdout
// ends.
func (pr *Proc) Lines() <-chan []byte { return pr.lines }

// ReadErr returns the error that ended stdout, or nil if stdout ended
// at EOF. Before stdout ends, it returns nil, so that a failed write can
// call it at once.
func (pr *Proc) ReadErr() error {
	select {
	case <-pr.readDone:
		return pr.readErr
	default:
		return nil
	}
}

// Stderr returns the end of the stderr of the CLI, or "" if there is
// none.
func (pr *Proc) Stderr() string {
	if pr.pipes.Stderr == nil {
		return ""
	}
	return pr.pipes.Stderr()
}

// ErrExited is the error of a CLI that exited while the agent read or
// wrote it.
var ErrExited = errors.New("the CLI exited")

// Exited returns the error of a CLI whose stdout ended: [ErrExited] with
// the error of the read, or with the end of stderr.
func (pr *Proc) Exited() error {
	if err := pr.ReadErr(); err != nil {
		return fmt.Errorf("%w: read the output: %w", ErrExited, err)
	}
	if text := pr.Stderr(); text != "" {
		return fmt.Errorf("%w: %s", ErrExited, text)
	}
	return ErrExited
}

// Write writes data to stdin. A CLI that rejects its flags exits at
// once, so the first write can fail before stdout ends. If stderr has
// text, the error is [ErrExited] with that text, because stderr says
// why.
func (pr *Proc) Write(data []byte) error {
	_, err := pr.pipes.Stdin.Write(data)
	if err == nil {
		return nil
	}
	if text := pr.Stderr(); text != "" {
		return fmt.Errorf("%w: %s", ErrExited, text)
	}
	return fmt.Errorf("write to the CLI: %w", err)
}

// Environ returns the environment of a CLI: the environment of this
// process without the variables named in drop, then extra.
func Environ(drop, extra []string) []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if slices.Contains(drop, name) {
			continue
		}
		env = append(env, kv)
	}
	return append(env, extra...)
}

// Stop ends the input of the CLI and waits for it to exit. Stop reads
// the remaining lines, so that the CLI does not block on a full pipe.
// If the CLI does not exit in [exitGrace], Stop sends SIGTERM, so that
// the CLI can write its session file. If it does not exit in
// [killTimeout] after that, Stop kills it. After the CLI exits, Stop
// kills each process that the CLI left, such as a command of a tool
// that runs in the background.
func (pr *Proc) Stop() error {
	pr.stopOnce.Do(func() {
		pr.pipes.Stdin.Close()
		go pr.drain()
		done := make(chan error, 1)
		go func() { done <- pr.pipes.Wait() }()
		defer pr.pipes.Kill()
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
