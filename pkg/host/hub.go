package host

import (
	"context"
	"errors"
	"iter"
	"sync"
)

// ErrSlowReader ends the stream of a reader that fell more than
// [maxQueued] events behind. The reader calls Events again, and gets a
// new state.
var ErrSlowReader = errors.New("host: the reader fell too far behind")

// maxQueued is the most events that a reader can hold. A reader that
// falls further behind ends with [ErrSlowReader], so that one stalled
// reader cannot hold the events in memory. A test sets it.
var maxQueued = 10_000

// hub keeps a state, and sends each event that changes it to its
// readers. A new reader gets a copy of the state and each event after
// that state, with no gap. The host has one hub for its sessions, and
// each session has one hub for its agent events.
type hub[S, E any] struct {
	// apply changes the state by one event.
	apply func(*S, E)
	// copy returns a copy of the state that a reader can keep.
	copy func(S) S

	mu      sync.Mutex
	state   S
	readers map[*reader[E]]struct{}
	closed  bool
}

func newHub[S, E any](state S, apply func(*S, E), copy func(S) S) *hub[S, E] {
	return &hub[S, E]{
		apply:   apply,
		copy:    copy,
		state:   state,
		readers: make(map[*reader[E]]struct{}),
	}
}

// publish applies e to the state and sends it to each reader. It drops
// a reader that fell too far behind. After close, publish does nothing.
func (h *hub[S, E]) publish(e E) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.apply(&h.state, e)
	for r := range h.readers {
		if !r.push(e) {
			delete(h.readers, r)
		}
	}
}

// subscribe returns a copy of the state, and the stream of the events
// after it. The stream ends when the hub closes, with the error of ctx
// when ctx ends, or with [ErrSlowReader]. Read the stream once.
func (h *hub[S, E]) subscribe(ctx context.Context) (S, iter.Seq2[E, error]) {
	r := &reader[E]{wake: make(chan struct{}, 1)}

	h.mu.Lock()
	state := h.copy(h.state)
	if h.closed {
		r.close()
	} else {
		h.readers[r] = struct{}{}
	}
	h.mu.Unlock()

	// A reader that never reads its stream leaves the hub when ctx ends.
	stop := context.AfterFunc(ctx, func() { h.remove(r) })

	events := func(yield func(E, error) bool) {
		defer stop()
		defer h.remove(r)
		var zero E
		for {
			select {
			case <-ctx.Done():
				yield(zero, ctx.Err())
				return
			case <-r.wake:
			}
			events, closed, slow := r.take()
			for _, e := range events {
				if !yield(e, nil) {
					return
				}
			}
			if slow {
				yield(zero, ErrSlowReader)
				return
			}
			if closed {
				return
			}
		}
	}
	return state, events
}

// close ends the stream of each reader. A reader gets the events that
// it holds first.
func (h *hub[S, E]) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for r := range h.readers {
		r.close()
	}
	clear(h.readers)
}

func (h *hub[S, E]) remove(r *reader[E]) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.readers, r)
}

// reader is one reader of a hub. The hub adds to its queue and never
// blocks on it.
type reader[E any] struct {
	mu     sync.Mutex
	queue  []E
	closed bool
	// slow is set when the queue passed maxQueued.
	slow bool
	// wake has one slot. A send tells the reader to take the queue.
	wake chan struct{}
}

// push adds e to the queue. It reports false if the reader fell too far
// behind, and its stream ends.
func (r *reader[E]) push(e E) bool {
	r.mu.Lock()
	if len(r.queue) >= maxQueued {
		r.slow = true
		r.closed = true
		r.mu.Unlock()
		r.signal()
		return false
	}
	r.queue = append(r.queue, e)
	r.mu.Unlock()
	r.signal()
	return true
}

func (r *reader[E]) close() {
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	r.signal()
}

func (r *reader[E]) signal() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// take returns the queued events, and reports if the stream ended and
// if the reader fell too far behind.
func (r *reader[E]) take() ([]E, bool, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	events := r.queue
	r.queue = nil
	return events, r.closed, r.slow
}
