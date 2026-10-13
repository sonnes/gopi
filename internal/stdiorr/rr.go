// Package stdiorr records and replays the stdio of one subprocess that
// speaks a line protocol, mainly for use in tests. It does for an agent
// CLI what package httprr does for an HTTP API.
//
// A trace file holds one line per message:
//
//	stdiorr trace v1
//	# a comment
//	> {"a line that the client wrote to stdin"}
//	< {"a line that the process wrote to stdout"}
//
// [Open] replays the file by default. In replay, each line that the
// client writes must match the next ">" line, after the scrub
// functions. After a match, the replay sends the "<" lines up to the
// next ">" line. The "<" lines before the first ">" line are sent at
// once. So the client must write the same lines in the same order on
// every run: request IDs must be deterministic.
//
// If the -clirecord flag matches the file name, Open starts the real
// process and writes a new file. The flag is defined only in test
// programs.
package stdiorr

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// header is the first line of a trace file.
const header = "stdiorr trace v1"

// maxLine is the longest line that the reader of a trace or of a
// recorded process accepts.
const maxLine = 16 << 20

var record = new(string)

func init() {
	if testing.Testing() {
		record = flag.String("clirecord", "", "re-record stdio traces for files matching `regexp`")
	}
}

// Scrub rewrites one line before it is compared or written to a trace,
// to remove secrets and values that change on every run, such as a
// working directory.
type Scrub func(line []byte) []byte

// Process is one subprocess as the client sees it. In replay, no
// process runs.
type Process struct {
	// Stdin is the input of the process. Close it to end the input.
	Stdin io.WriteCloser
	// Stdout is the output of the process.
	Stdout io.ReadCloser
	// Wait waits until the process exits.
	Wait func() error
	// Kill stops the process.
	Kill func() error
}

// Recording reports whether the -clirecord flag matches file.
func Recording(file string) (bool, error) {
	if *record == "" {
		return false, nil
	}
	re, err := regexp.Compile(*record)
	if err != nil {
		return false, fmt.Errorf("stdiorr: invalid -clirecord flag: %w", err)
	}
	return re.MatchString(file), nil
}

// Open returns the process for cmd, backed by the trace in file. In
// replay, Open reads file and does not start cmd. In record mode, Open
// starts cmd and writes each line of its stdin and stdout to file.
// Stderr of the process goes to the stderr of the test.
//
// In replay, a line from the client that does not match the trace
// fails t, and stdout ends, so the client stops.
func Open(t testing.TB, file string, cmd *exec.Cmd, scrubs ...Scrub) (*Process, error) {
	t.Helper()
	recording, err := Recording(file)
	if err != nil {
		return nil, err
	}
	if recording {
		return startRecord(file, cmd, scrubs)
	}
	return startReplay(t, file, scrubs)
}

// scrub applies scrubs to line in order.
func scrub(line []byte, scrubs []Scrub) []byte {
	for _, s := range scrubs {
		line = s(line)
	}
	return line
}

// startRecord starts cmd and writes its stdio to a new trace in file.
func startRecord(file string, cmd *exec.Cmd, scrubs []Scrub) (*Process, error) {
	f, err := os.Create(file)
	if err != nil {
		return nil, err
	}
	if _, err := fmt.Fprintln(f, header); err != nil {
		f.Close()
		return nil, err
	}
	w := &traceWriter{
		f:      f,
		scrubs: scrubs,
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		f.Close()
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		f.Close()
		return nil, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		f.Close()
		return nil, err
	}

	outR, outW := io.Pipe()
	copied := make(chan struct{})
	go func() {
		defer close(copied)
		sc := bufio.NewScanner(stdout)
		sc.Buffer(nil, maxLine)
		for sc.Scan() {
			line := sc.Bytes()
			w.write('<', line)
			if _, err := outW.Write(append(line, '\n')); err != nil {
				break
			}
		}
		outW.CloseWithError(sc.Err())
	}()

	wait := func() error {
		<-copied
		err := cmd.Wait()
		if cerr := w.close(); err == nil {
			err = cerr
		}
		return err
	}
	kill := func() error {
		return cmd.Process.Kill()
	}
	return &Process{
		Stdin: &lineWriter{
			line: func(line []byte) error {
				w.write('>', line)
				_, err := stdin.Write(append(line, '\n'))
				return err
			},
			close: stdin.Close,
		},
		Stdout: outR,
		Wait:   wait,
		Kill:   kill,
	}, nil
}

// traceWriter writes the lines of a recording to a trace file. The
// stdin and stdout of the process write from two goroutines.
type traceWriter struct {
	mu     sync.Mutex
	f      *os.File
	scrubs []Scrub
	err    error
}

func (w *traceWriter) write(dir byte, line []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return
	}
	line = scrub(bytes.Clone(line), w.scrubs)
	_, w.err = fmt.Fprintf(w.f, "%c %s\n", dir, line)
}

func (w *traceWriter) close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return w.err
	}
	err := w.f.Close()
	w.f = nil
	if w.err != nil {
		return w.err
	}
	return err
}

// step is one line of a trace.
type step struct {
	// in is true for a line to the process.
	in   bool
	line []byte
}

// readTrace reads the steps of the trace in file.
func readTrace(file string) ([]step, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(nil, maxLine)
	if !sc.Scan() || sc.Text() != header {
		return nil, fmt.Errorf("stdiorr: %s: the first line is not %q", file, header)
	}
	var steps []step
	for n := 2; sc.Scan(); n++ {
		line := sc.Text()
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
			continue
		case strings.HasPrefix(line, "> "):
			steps = append(steps, step{in: true, line: []byte(line[2:])})
		case strings.HasPrefix(line, "< "):
			steps = append(steps, step{in: false, line: []byte(line[2:])})
		default:
			return nil, fmt.Errorf("stdiorr: %s:%d: a line starts with \"> \" or \"< \"", file, n)
		}
	}
	return steps, sc.Err()
}

// errMismatch ends stdout when the client writes a line that the trace
// does not expect.
var errMismatch = errors.New("stdiorr: the client wrote a line that the trace does not expect")

// startReplay returns a process that replays the trace in file.
func startReplay(t testing.TB, file string, scrubs []Scrub) (*Process, error) {
	steps, err := readTrace(file)
	if err != nil {
		return nil, err
	}
	r := &replay{
		t:      t,
		file:   file,
		steps:  steps,
		scrubs: scrubs,
		lines:  make(chan []byte, len(steps)+1),
		done:   make(chan struct{}),
	}
	outR, outW := io.Pipe()
	go r.serve(outW)
	r.flush()

	stop := func() error {
		r.stop()
		outR.Close()
		return nil
	}
	return &Process{
		Stdin: &lineWriter{
			line:  r.write,
			close: func() error { r.stop(); return nil },
		},
		Stdout: outR,
		Wait: func() error {
			<-r.done
			return nil
		},
		Kill: stop,
	}, nil
}

// replay plays a trace. write takes lines from the client, and serve
// sends the lines of the process on stdout.
type replay struct {
	t      testing.TB
	file   string
	scrubs []Scrub

	mu    sync.Mutex
	steps []step
	ended bool

	// lines carries the "<" lines to serve. A nil line ends stdout with
	// errMismatch.
	lines    chan []byte
	done     chan struct{}
	stopOnce sync.Once
}

// write matches one line from the client with the next ">" line, then
// queues the "<" lines that follow it.
func (r *replay) write(line []byte) error {
	r.mu.Lock()
	if r.ended {
		r.mu.Unlock()
		return io.ErrClosedPipe
	}
	got := scrub(bytes.Clone(line), r.scrubs)
	if len(r.steps) == 0 || !r.steps[0].in || !bytes.Equal(r.steps[0].line, got) {
		want := "the end of the trace"
		if len(r.steps) > 0 && r.steps[0].in {
			want = string(r.steps[0].line)
		}
		r.mu.Unlock()
		r.t.Errorf("stdiorr: %s: unexpected line from the client\ngot:  %s\nwant: %s", r.file, got, want)
		r.fail()
		return errMismatch
	}
	r.steps = r.steps[1:]
	r.mu.Unlock()
	r.flush()
	return nil
}

// flush queues the "<" lines up to the next ">" line.
func (r *replay) flush() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for len(r.steps) > 0 && !r.steps[0].in {
		r.lines <- r.steps[0].line
		r.steps = r.steps[1:]
	}
	if len(r.steps) == 0 {
		r.end()
	}
}

// fail ends stdout with errMismatch.
func (r *replay) fail() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ended {
		return
	}
	r.lines <- nil
	r.end()
}

// end closes the queue. The caller holds mu.
func (r *replay) end() {
	if r.ended {
		return
	}
	r.ended = true
	close(r.lines)
}

// stop ends the replay, as if the process exited.
func (r *replay) stop() {
	r.stopOnce.Do(func() {
		r.mu.Lock()
		r.end()
		r.mu.Unlock()
	})
}

// serve writes the queued lines to w. It closes w at the end of the
// trace, so the client reads EOF.
func (r *replay) serve(w *io.PipeWriter) {
	defer close(r.done)
	for line := range r.lines {
		if line == nil {
			w.CloseWithError(errMismatch)
			return
		}
		if _, err := w.Write(append(line, '\n')); err != nil {
			return
		}
	}
	w.Close()
}

// lineWriter splits the writes of the client into lines.
type lineWriter struct {
	mu    sync.Mutex
	buf   []byte
	line  func([]byte) error
	close func() error
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		line := bytes.Clone(w.buf[:i])
		w.buf = w.buf[i+1:]
		if err := w.line(line); err != nil {
			return 0, err
		}
	}
}

func (w *lineWriter) Close() error {
	return w.close()
}
