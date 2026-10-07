package ai

import "github.com/sonnes/gopi/pkg/stream"

// EventStream is the stream of [Event] values from one model call. It
// ends with the final [Message]. Events reads the events in order. Wait
// waits for the final message. An error from the producer appears on
// both.
type EventStream = stream.Stream[Event, *Message]

// NewEventStream runs fn in a goroutine. fn pushes events through the
// callback and returns the final message. A [TextProvider] starts its
// stream here.
func NewEventStream(fn func(push func(Event)) (*Message, error)) *EventStream {
	return stream.New(fn)
}

// ErrStream returns an [EventStream] that fails immediately with err. A
// provider uses it to report an error from before the request on the
// stream, instead of through a second return value.
func ErrStream(err error) *EventStream {
	return stream.Err[Event, *Message](err)
}
