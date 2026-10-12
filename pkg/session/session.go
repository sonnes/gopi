package session

import "time"

// Session records that a session exists. It carries no application
// state. A title, a mode, or a model choice goes in a [Value].
//
// The caller fills every field, and the store writes the record as it
// is. A store has no clock of its own, so two stores write the same
// bytes for the same record.
type Session struct {
	// ID identifies the session. The application chooses what it means:
	// a user ID, a ticket number, or a random string.
	ID string `json:"id"`

	// CreatedAt is the time of creation.
	CreatedAt time.Time `json:"created_at"`
}
