package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"sync"
	"time"
)

// input holds the lines of stdin. A goroutine reads stdin into a queue
// as the lines come, with the time of each, so a line typed during a
// run waits in the queue. The prompt loop takes the first line.
// ask_user_question takes the first line that came after it showed its
// question, so a line typed before the question stays for the prompt
// loop.
type input struct {
	// interactive is true when stdin is a terminal. From a pipe or a
	// file, every line is an answer, whenever it came.
	interactive bool

	mu    sync.Mutex
	queue []line
	eof   bool
	// changed is closed and replaced each time the queue changes.
	changed chan struct{}
}

// line is one line of stdin and the time that it came.
type line struct {
	text string
	at   time.Time
}

// readInput starts to read r line by line, and returns the input.
func readInput(r io.Reader, interactive bool) *input {
	in := &input{
		interactive: interactive,
		changed:     make(chan struct{}),
	}
	go func() {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			in.push(line{text: scanner.Text(), at: time.Now()})
		}
		if err := scanner.Err(); err != nil {
			fmt.Fprintln(os.Stderr, "gopi:", err)
		}
		in.end()
	}()
	return in
}

func (in *input) push(l line) {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.queue = append(in.queue, l)
	in.notify()
}

func (in *input) end() {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.eof = true
	in.notify()
}

// notify wakes each reader that waits. The caller holds mu.
func (in *input) notify() {
	close(in.changed)
	in.changed = make(chan struct{})
}

// next returns the first line. ok is false at the end of the input.
func (in *input) next() (text string, ok bool) {
	for {
		in.mu.Lock()
		if len(in.queue) > 0 {
			l := in.queue[0]
			in.queue = in.queue[1:]
			in.mu.Unlock()
			return l.text, true
		}
		if in.eof {
			in.mu.Unlock()
			return "", false
		}
		changed := in.changed
		in.mu.Unlock()
		<-changed
	}
}

// after returns the first line that came after t. On a terminal, a line
// that came before t was typed before the question, so it stays for
// [input.next]. When ctx ends, after returns its error and takes no
// line.
func (in *input) after(ctx context.Context, t time.Time) (string, error) {
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		in.mu.Lock()
		for i, l := range in.queue {
			if in.interactive && l.at.Before(t) {
				continue
			}
			in.queue = slices.Delete(in.queue, i, i+1)
			in.mu.Unlock()
			return l.text, nil
		}
		if in.eof {
			in.mu.Unlock()
			return "", errors.New("no answer: the input ended")
		}
		changed := in.changed
		in.mu.Unlock()

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-changed:
		}
	}
}

// shownCalls records the tool calls that the tree printed. The tree
// prints the events of a run on one goroutine, and a tool asks its
// question on another, so a question waits for its call to show first.
type shownCalls struct {
	mu   sync.Mutex
	done map[string]chan struct{}
}

// channel returns the channel of id, which mark closes. The caller
// holds mu.
func (s *shownCalls) channel(id string) chan struct{} {
	if s.done == nil {
		s.done = make(map[string]chan struct{})
	}
	c, ok := s.done[id]
	if !ok {
		c = make(chan struct{})
		s.done[id] = c
	}
	return c
}

// mark records that the tree printed the call id.
func (s *shownCalls) mark(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.channel(id)
	select {
	case <-c:
	default:
		close(c)
	}
}

// wait returns when the tree printed the call id, or when ctx ends. A
// nil shownCalls does not wait.
func (s *shownCalls) wait(ctx context.Context, id string) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	c := s.channel(id)
	s.mu.Unlock()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c:
		return nil
	}
}
