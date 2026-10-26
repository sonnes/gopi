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
// that state, with no gap. The host has one hub for its
// sessions, and each session has one hub for its agent events.
type hub[S, E any] struct {
	// apply changes the state by one event.
	apply func(*S, E)
	// copy returns a copy of the state that a reader can keep.
	copy func(S) S

	// mu also guards the fields of each reader.
	mu      sync.Mutex
	state   S
	readers map[*reader[E]]struct{}
	closed  bool
}

// reader is one reader of a hub. The hub adds to its queue and never
// blocks on it.
type reader[E any] struct {
	queue []E
	// ended is set when the hub closes or drops the reader. slow is set
	// when the queue passed maxQueued.
	ended bool
	slow  bool
	// wake has one slot. A send tells the reader to take the queue.
	wake chan struct{}
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
		if len(r.queue) >= maxQueued {
			r.slow = true
			h.endLocked(r)
			continue
		}
		r.queue = append(r.queue, e)
		r.signal()
	}
}

// reset replaces the state. A reader that joined before keeps its own
// state, and gets the events after the reset.
func (h *hub[S, E]) reset(state S) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.state = state
}

// subscribe returns a copy of the state, and the stream of the events
// after it. The stream ends when the hub closes, with the error of ctx
// when ctx ends, or with [ErrSlowReader]. Read the stream once.
func (h *hub[S, E]) subscribe(ctx context.Context) (S, iter.Seq2[E, error]) {
	r := &reader[E]{wake: make(chan struct{}, 1)}

	h.mu.Lock()
	state := h.copy(h.state)
	h.readers[r] = struct{}{}
	if h.closed {
		h.endLocked(r)
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
			events, ended, slow := h.take(r)
			for _, e := range events {
				if !yield(e, nil) {
					return
				}
			}
			if slow {
				yield(zero, ErrSlowReader)
				return
			}
			if ended {
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
		h.endLocked(r)
	}
}

// endLocked ends the stream of r, and removes r from the hub.
func (h *hub[S, E]) endLocked(r *reader[E]) {
	r.ended = true
	r.signal()
	delete(h.readers, r)
}

// remove removes r from the hub.
func (h *hub[S, E]) remove(r *reader[E]) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.readers, r)
}

// take returns the queued events of r, and reports if the stream ended
// and if r fell too far behind.
func (h *hub[S, E]) take(r *reader[E]) ([]E, bool, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	events := r.queue
	r.queue = nil
	return events, r.ended, r.slow
}

// signal tells the reader to take its queue.
func (r *reader[E]) signal() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}
