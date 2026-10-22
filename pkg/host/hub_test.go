package host

import (
	"context"
	"iter"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sumHub returns a hub whose state is the sum of its events.
func sumHub() *hub[int, int] {
	return newHub(
		0,
		func(s *int, e int) { *s += e },
		func(s int) int { return s },
	)
}

// collect reads the stream until it ends, and returns its events and
// its error.
func collect(t *testing.T, events iter.Seq2[int, error]) ([]int, error) {
	t.Helper()
	var out []int
	for e, err := range events {
		if err != nil {
			return out, err
		}
		out = append(out, e)
	}
	return out, nil
}

func TestHubStateThenEvents(t *testing.T) {
	h := sumHub()
	h.publish(1)
	state, events := h.subscribe(context.Background())
	h.publish(2)
	h.publish(3)
	h.close()

	got, err := collect(t, events)
	require.NoError(t, err)
	assert.Equal(t, 1, state)
	assert.Equal(t, []int{2, 3}, got)
}

func TestHubSlowReader(t *testing.T) {
	old := maxQueued
	maxQueued = 2
	t.Cleanup(func() { maxQueued = old })

	h := sumHub()
	_, slow := h.subscribe(context.Background())
	_, fast := h.subscribe(context.Background())

	got := make(chan int)
	go func() {
		for e, err := range fast {
			if err != nil {
				return
			}
			got <- e
		}
		close(got)
	}()
	var fastGot []int
	for i := range 5 {
		h.publish(i)
		fastGot = append(fastGot, <-got)
	}
	h.close()

	slowGot, err := collect(t, slow)
	assert.ErrorIs(t, err, ErrSlowReader)
	assert.Equal(t, []int{0, 1}, slowGot)
	assert.Equal(t, []int{0, 1, 2, 3, 4}, fastGot)
	_, open := <-got
	assert.False(t, open)
}

func TestHubContextEnds(t *testing.T) {
	h := sumHub()
	ctx, cancel := context.WithCancel(context.Background())
	_, events := h.subscribe(ctx)
	cancel()

	_, err := collect(t, events)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 0, readerCount(h))
}

func TestHubClose(t *testing.T) {
	h := sumHub()
	_, before := h.subscribe(context.Background())
	h.close()
	h.publish(1)
	state, after := h.subscribe(context.Background())

	got, err := collect(t, before)
	require.NoError(t, err)
	assert.Empty(t, got)
	got, err = collect(t, after)
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Equal(t, 0, state)
}

func TestHubStopReading(t *testing.T) {
	h := sumHub()
	_, events := h.subscribe(context.Background())
	h.publish(1)
	h.publish(2)

	for e, err := range events {
		require.NoError(t, err)
		assert.Equal(t, 1, e)
		break
	}
	assert.Equal(t, 0, readerCount(h))
}

// readerCount returns the number of readers of h.
func readerCount[S, E any](h *hub[S, E]) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.readers)
}
