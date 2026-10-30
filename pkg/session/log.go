package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/sonnes/gopi/pkg/ai"
)

// entryIDBytes is the length of the random part of an entry ID.
const entryIDBytes = 8

// interruptedText is the text of [Interrupted].
const interruptedText = "tool execution interrupted"

// Interrupted returns the error result of a tool call that did not
// finish. [Log.Messages] gives it to each tool call that has no result,
// because a provider rejects a call with no result.
func Interrupted(callID string) ai.Message {
	m := ai.ToolResultMessage(callID, ai.Text{Text: interruptedText})
	m.IsError = true
	return m
}

// Log is a live handle on one session. It holds the record, the entries
// in append order, and the values. It is safe for concurrent use.
//
// A session has one writer. Two logs on one session interleave their
// entries, and the result can be a conversation that a provider
// rejects.
type Log struct {
	store   Storer
	session Session

	mu      sync.Mutex
	entries []Entry
	values  map[address]json.RawMessage
}

// Reader reads a log: its session, its entries, and its values. A
// [Log] is a Reader. [Value.Get] reads a value through it.
type Reader interface {
	Session() Session
	Entries() []Entry
	Messages() []ai.Message
	value(addr address) (json.RawMessage, bool)
}

// Writer is a [Reader] that also sets values. A [Log] is a Writer.
// [Log.Writer] returns one that cannot append entries, for a caller
// that must not add to the log, such as a hook of an agent. The
// agent appends each entry of a run, so that each message gets its
// events in order. [Value.Set] and [Value.Delete] write through a
// Writer.
type Writer interface {
	Reader
	setValue(ctx context.Context, addr address, data json.RawMessage) error
	deleteValue(ctx context.Context, addr address) error
}

// Writer returns l as a [Writer] without [Log.Append]. The log is in a
// field and not embedded, so a type assertion cannot find Append.
func (l *Log) Writer() Writer { return writer{log: l} }

// writer is the [Writer] of [Log.Writer].
type writer struct {
	log *Log
}

func (w writer) Session() Session       { return w.log.Session() }
func (w writer) Entries() []Entry       { return w.log.Entries() }
func (w writer) Messages() []ai.Message { return w.log.Messages() }

func (w writer) value(addr address) (json.RawMessage, bool) {
	return w.log.value(addr)
}

func (w writer) setValue(ctx context.Context, addr address, data json.RawMessage) error {
	return w.log.setValue(ctx, addr, data)
}

func (w writer) deleteValue(ctx context.Context, addr address) error {
	return w.log.deleteValue(ctx, addr)
}

// Open creates or resumes the session id in store. If id is unknown,
// Open creates it. If id exists, Open loads its entries and values.
// There is no separate resume, because both paths give the same ready
// log. An empty id is an error.
func Open(ctx context.Context, store Storer, id string) (*Log, error) {
	if id == "" {
		return nil, errors.New("session: id is required")
	}

	snap, err := store.Load(ctx, id)
	if errors.Is(err, ErrNotFound) {
		snap = Snapshot{
			Session: Session{
				ID:        id,
				CreatedAt: time.Now(),
			},
		}
		// Another caller can create id between the Load and the Create.
		// Then its session is the one to resume.
		cerr := store.Create(ctx, snap.Session)
		if errors.Is(cerr, ErrExists) {
			snap, cerr = store.Load(ctx, id)
		}
		if cerr != nil {
			return nil, cerr
		}
	} else if err != nil {
		return nil, err
	}

	return newLog(store, snap), nil
}

// New returns an unsaved log over a fresh [MemoryStore]. Its session
// has no ID, and its entries end with the process.
func New() *Log {
	store := NewMemoryStore()
	rec := Session{CreatedAt: time.Now()}
	// A fresh memory store cannot refuse a record.
	_ = store.Create(context.Background(), rec)
	return newLog(store, Snapshot{Session: rec})
}

// newLog returns a log over the contents of snap.
func newLog(store Storer, snap Snapshot) *Log {
	values := make(map[address]json.RawMessage, len(snap.Values))
	for _, v := range snap.Values {
		addr := address{
			namespace: v.Namespace,
			key:       v.Key,
		}
		values[addr] = v.Data
	}
	return &Log{
		store:   store,
		session: snap.Session,
		entries: snap.Entries,
		values:  values,
	}
}

// Session returns the record.
func (l *Log) Session() Session { return l.session }

// Entries returns the stored entries in append order. The slice is a
// copy.
func (l *Log) Entries() []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.entries)
}

// Messages returns the messages of the entries, in order: the full
// history. It skips each custom entry, and it does not apply a
// [Compaction]. [History] gives the IDs and the edits too.
//
// A crash or a canceled run between an assistant message and its tool
// results leaves a tool call with no result. A provider rejects that.
// Messages gives each such call the result of [Interrupted]. The repair lives here
// because a dangling call is a fact about the stored log, not about the
// loop that reads it.
func (l *Log) Messages() []ai.Message {
	entries := l.History().Entries
	msgs := make([]ai.Message, 0, len(entries))
	for _, e := range entries {
		msgs = append(msgs, e.Message)
	}
	return msgs
}

// History is what an agent reads to resume a session.
type History struct {
	// Entries are the message entries, in order, with the repairs of
	// [Log.Messages]. A repaired result has the fixed ID
	// "<entry of the call>/<ID of the call>", so an edit can name it.
	Entries []Entry
	// Edits are the edits of each [Compaction], in order. Apply them to
	// Entries with [Fold].
	Edits []Edit
	// CompactedAt is the index in Entries of the first message after the
	// last compaction. It is 0 if the log has no compaction. The usage
	// of a message before it reports the size before the compaction.
	CompactedAt int
}

// History returns the messages and the compactions of the log. It skips
// a compaction that does not decode, because the full history is valid.
func (l *Log) History() History {
	var (
		h        History
		messages []Entry
		// compacted is the number of stored messages before the last
		// compaction.
		compacted int
	)
	for _, e := range l.Entries() {
		if e.Kind == "" {
			messages = append(messages, e)
			continue
		}
		if e.Kind != KindCompaction {
			continue
		}
		var c Compaction
		if err := json.Unmarshal(e.Data, &c); err != nil {
			continue
		}
		h.Edits = append(h.Edits, c.Edits...)
		compacted = len(messages)
	}
	h.Entries, h.CompactedAt = repairToolCalls(messages, compacted)
	return h
}

// repairToolCalls appends an error result after each assistant message
// for every tool call that has no result. It returns the entries, and
// the index that the entry at mark moves to.
func repairToolCalls(entries []Entry, mark int) ([]Entry, int) {
	out := make([]Entry, 0, len(entries))
	moved := mark
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		out = append(out, e)
		if e.Message.Role != ai.RoleAssistant {
			continue
		}

		calls := e.Message.ToolCalls()
		if len(calls) == 0 {
			continue
		}
		answered := make(map[string]bool, len(calls))
		for i+1 < len(entries) && entries[i+1].Message.Role == ai.RoleToolResult {
			i++
			out = append(out, entries[i])
			answered[entries[i].Message.ToolCallID] = true
		}
		for _, tc := range calls {
			if answered[tc.ID] {
				continue
			}
			out = append(out, Entry{
				ID:      e.ID + "/" + tc.ID,
				Message: Interrupted(tc.ID),
			})
			if i < mark {
				moved++
			}
		}
	}
	return out, moved
}

// Append sets ID and CreatedAt on each entry, writes the entries to the
// store, and adds them to the log. It returns every entry with its
// fields set, in the order given.
//
// An ephemeral entry gets an ID, and Append returns it, but Append
// never writes it and the log does not keep it.
//
// If the store fails, the log does not change.
func (l *Log) Append(ctx context.Context, entries ...Entry) ([]Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	out := make([]Entry, 0, len(entries))
	stored := make([]Entry, 0, len(entries))
	for _, e := range entries {
		e.ID = newEntryID()
		e.CreatedAt = now
		out = append(out, e)

		if e.Ephemeral {
			continue
		}
		stored = append(stored, e)
	}

	if len(stored) > 0 {
		if err := l.store.Append(ctx, l.session.ID, stored...); err != nil {
			return nil, err
		}
	}

	l.entries = append(l.entries, stored...)
	return out, nil
}

// value returns the stored bytes at addr.
func (l *Log) value(addr address) (json.RawMessage, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	data, ok := l.values[addr]
	return data, ok
}

// setValue writes data at addr through the store. If the store fails,
// the value does not change.
func (l *Log) setValue(ctx context.Context, addr address, data json.RawMessage) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	v := StoredValue{
		Namespace: addr.namespace,
		Key:       addr.key,
		Data:      data,
	}
	if err := l.store.SetValue(ctx, l.session.ID, v); err != nil {
		return err
	}
	l.values[addr] = data
	return nil
}

// deleteValue removes the value at addr through the store. If the
// store fails, the value does not change.
func (l *Log) deleteValue(ctx context.Context, addr address) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.store.DeleteValue(ctx, l.session.ID, addr.namespace, addr.key); err != nil {
		return err
	}
	delete(l.values, addr)
	return nil
}

// newEntryID returns a random hex ID.
func newEntryID() string {
	b := make([]byte, entryIDBytes)
	if _, err := rand.Read(b); err != nil {
		panic("session: crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}
