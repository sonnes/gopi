package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/sonnes/gopi/pkg/ai"
)

// Entry is one item of the log: one message and who wrote it, or one
// custom entry. An Entry round-trips through JSON, so a store keeps it
// without a schema.
//
// The log sets ID and CreatedAt on [Log.Append]. The caller sets Author
// and Ephemeral. Author and Ephemeral live here and not on [ai.Message]
// because a provider never reads them. They are facts about the
// application and the store.
type Entry struct {
	// ID identifies the entry in its session.
	ID string `json:"id"`
	// CreatedAt is the time of the append.
	CreatedAt time.Time `json:"created_at"`

	// Author names who wrote the message: a user ID, an agent name, or
	// an injector. Empty means unknown.
	Author string `json:"author,omitempty"`
	// Ephemeral marks context that is true only now, such as a reminder
	// built from live state. The model reads it in this run. The store
	// skips it, so the next run does not read it.
	Ephemeral bool `json:"ephemeral,omitempty"`

	// Kind names a custom entry, such as "compaction". Empty means a
	// message entry. The kind "gopi" and every kind that starts with
	// "gopi." are reserved for this module.
	Kind string `json:"kind,omitempty"`

	// Message is the message of a message entry. A custom entry has
	// none.
	Message ai.Message `json:"message,omitzero"`

	// Data is the content of a custom entry. The model never reads it.
	Data json.RawMessage `json:"data,omitempty"`
}

// Custom returns a custom entry of kind with data encoded as JSON. The
// log stores a custom entry in its place among the messages, and
// [Log.Messages] skips it. Use one for a fact that the application
// reads back in order, such as the summary of a compaction. If kind is
// empty, or data does not encode, Custom returns an error.
func Custom(kind string, data any, opts ...Option) (Entry, error) {
	if kind == "" {
		return Entry{}, errors.New("session: custom entry kind is required")
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return Entry{}, fmt.Errorf("session: encode custom entry %q: %w", kind, err)
	}
	e := Entry{
		Kind: kind,
		Data: raw,
	}
	for _, opt := range opts {
		opt(&e)
	}
	return e, nil
}

// Option sets a field of a new [Entry]. [Text] and [NewEntry] take
// options.
type Option func(*Entry)

// Author sets the author of the entry.
func Author(id string) Option {
	return func(e *Entry) { e.Author = id }
}

// Ephemeral marks the entry as context that the store skips.
func Ephemeral() Option {
	return func(e *Entry) { e.Ephemeral = true }
}

// NewEntry returns an entry for m with opts applied. The log sets ID
// and CreatedAt on append.
func NewEntry(m ai.Message, opts ...Option) Entry {
	e := Entry{Message: m}
	for _, opt := range opts {
		opt(&e)
	}
	return e
}

// Text returns an entry with one user text message.
func Text(s string, opts ...Option) Entry {
	return NewEntry(ai.UserMessage(s), opts...)
}
