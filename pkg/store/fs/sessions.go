package fs

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/sonnes/gopi/pkg/session"
)

const (
	// typeSession tags the first line of a file.
	typeSession = "session_init"
	// typeMessage tags an entry line.
	typeMessage = "message"
	// typeValue tags a value line.
	typeValue = "value"

	// opSet and opDelete are the ops of a value line.
	opSet    = "set"
	opDelete = "delete"

	// sessionExt is the extension of the file of a session.
	sessionExt = ".jsonl"

	dirPerm  = 0o755
	filePerm = 0o644
)

// recordLine is the wire shape of the first line: the type and the
// record, flat. The record and the entry have their own shapes, because
// they share field names, and one struct that inlines both loses them.
type recordLine struct {
	Type string `json:"type"`
	session.Session
}

// entryLine is the wire shape of every later line: the type and the
// entry, flat.
type entryLine struct {
	Type string `json:"type"`
	session.Entry
}

// errTornRecord marks a first line with no newline that does not
// decode: a crash stopped [SessionStore.Create] before the record was whole.
var errTornRecord = errors.New("torn session record")

// lineType is the part of a line that Load reads first, to choose the
// shape of the rest.
type lineType struct {
	Type string `json:"type"`
}

// valueLine is the wire shape of a value line. A delete has no value.
type valueLine struct {
	Type      string          `json:"type"`
	Op        string          `json:"op"`
	Namespace string          `json:"namespace"`
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value,omitempty"`
}

// write writes b to f. A test replaces it to make a write stop in the
// middle, as a full disk does.
var write = (*os.File).Write

// SessionStore keeps each session as one append-only JSONL file under
// a root directory. It is safe for concurrent use in one process.
//
// The file of a session is <root>/<id>.jsonl. The first line is the
// [session.Session] record with the type "session_init". Each later
// line is one [session.Entry] with the type "message", or one value
// write with the type "value". A custom entry has the type "message"
// too, so a file of an older version loads as it is. The fields sit
// next to the type, flat. The store never rewrites a line. Only
// [SessionStore.Load] changes the end of the file: it cuts a torn last
// line, or adds the newline to a whole one, so the next append starts
// on a new line. So call Load only from the one writer of a session,
// or when no writer runs. A Load in a second process can cut a line
// that the writer has not finished.
//
// A value write has the op "set" or "delete". Load replays the value
// lines in file order, so a later set replaces an earlier one. Each set
// adds a line, so a value that changes often makes the file grow.
//
// A session ID is a file name, so it must not be empty, ".", "..", or
// hold a path separator.
type SessionStore struct {
	mu   sync.Mutex
	root string
}

var _ session.Storer = (*SessionStore)(nil)

// Sessions returns a [SessionStore] rooted at dir. If the directory
// does not exist, Sessions creates it.
func Sessions(dir string) (*SessionStore, error) {
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return nil, err
	}
	return &SessionStore{root: dir}, nil
}

// Create implements [session.Storer]. It writes the record as the first
// line of a new file. The exclusive create makes the existence check
// atomic. A file with a torn record holds nothing to recover, so Create
// replaces it. If the write fails, Create removes the file, so a later
// Create can use the ID.
func (s *SessionStore) Create(_ context.Context, rec session.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path, err := s.path(rec.ID)
	if err != nil {
		return err
	}
	data, err := json.Marshal(recordLine{
		Type:    typeSession,
		Session: rec,
	})
	if err != nil {
		return err
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, filePerm)
	if errors.Is(err, os.ErrExist) {
		f, err = openTorn(path)
	}
	if err != nil {
		return err
	}
	if _, err := write(f, append(data, '\n')); err != nil {
		f.Close()
		return errors.Join(err, os.Remove(path))
	}
	return f.Close()
}

// openTorn opens the file at path to write a new record, if the file
// has a torn record. Otherwise it returns [session.ErrExists].
func openTorn(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR, filePerm)
	if err != nil {
		return nil, err
	}
	_, _, err = readRecord(bufio.NewReader(f), filepath.Base(path))
	if !errors.Is(err, errTornRecord) {
		f.Close()
		return nil, session.ErrExists
	}
	if err := f.Truncate(0); err != nil {
		f.Close()
		return nil, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// Load implements [session.Storer]. A file with a torn record is a
// Create that a crash stopped, so Load returns [session.ErrNotFound].
//
// If the last line has no newline and does not decode, Load ignores it
// as a torn write, and cuts it from the file. If it decodes, Load adds
// its newline. Either way the next append starts on a new line. A line
// that ends with a newline and does not decode is corruption, and Load
// returns an error.
func (s *SessionStore) Load(_ context.Context, id string) (session.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	path, err := s.path(id)
	if err != nil {
		return session.Snapshot{}, err
	}
	return readSession(path, id, true)
}

// List returns the record and the values of each session in the root,
// in the order of creation, then of ID. Entries is nil in each
// snapshot. List reads each file to its end, because a value line can
// come after any entry. It skips each file that it cannot read as a
// session: a file of another kind, a torn record, or a corrupt line.
// One bad file then does not hide the other sessions. [SessionStore.Load]
// of the ID gives the error of the file. List changes no file, so it
// can run while another process writes a session.
func (s *SessionStore) List(ctx context.Context) ([]session.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	files, err := os.ReadDir(s.root)
	if err != nil {
		return nil, err
	}

	var out []session.Snapshot
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		id, isSession := strings.CutSuffix(f.Name(), sessionExt)
		if !isSession || !f.Type().IsRegular() {
			continue
		}
		path, err := s.path(id)
		if err != nil {
			continue
		}

		snap, err := readSession(path, id, false)
		if err != nil {
			continue
		}
		snap.Entries = nil
		out = append(out, snap)
	}

	slices.SortStableFunc(out, func(a, b session.Snapshot) int {
		return cmp.Or(
			a.Session.CreatedAt.Compare(b.Session.CreatedAt),
			cmp.Compare(a.Session.ID, b.Session.ID),
		)
	})
	return out, nil
}

// readSession reads the file of the session id at path. If repair is
// true, it ends the file on a newline, as [SessionStore.Load] says.
func readSession(path, id string, repair bool) (session.Snapshot, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return session.Snapshot{}, session.ErrNotFound
	}
	if err != nil {
		return session.Snapshot{}, err
	}
	defer f.Close()

	r := bufio.NewReader(f)
	rec, raw, err := readRecord(r, id)
	if errors.Is(err, errTornRecord) {
		return session.Snapshot{}, session.ErrNotFound
	}
	if err != nil {
		return session.Snapshot{}, err
	}
	// A crash before the newline of the record leaves a whole record
	// with no newline. It gets its newline, as a whole last line does.
	if repair && !bytes.HasSuffix(raw, []byte{'\n'}) {
		if err := endLastLine(path, 0, raw, true); err != nil {
			return session.Snapshot{}, fmt.Errorf("fs: repair %q: %w", id, err)
		}
	}
	size := int64(len(raw))

	var entries []session.Entry
	values := newReplay()
	// whole is the size of the lines that end with a newline.
	whole := size
	for {
		raw, err := r.ReadBytes('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return session.Snapshot{}, fmt.Errorf("fs: read %q: %w", id, err)
		}
		// ReadBytes stops short of a newline only at the end of the
		// file, so io.EOF marks the one line a torn write can leave.
		last := errors.Is(err, io.EOF)
		if !last {
			whole += int64(len(raw))
		}

		var derr error
		if line := bytes.TrimSpace(raw); len(line) > 0 {
			derr = decodeLine(line, &entries, values)
		}
		if derr != nil && !last {
			return session.Snapshot{}, fmt.Errorf("fs: decode line in %q: %w", id, derr)
		}
		if last && repair {
			if err := endLastLine(path, whole, raw, derr == nil); err != nil {
				return session.Snapshot{}, fmt.Errorf("fs: repair %q: %w", id, err)
			}
		}
		if last {
			break
		}
	}

	snap := session.Snapshot{
		Session: rec,
		Entries: entries,
		Values:  values.current(),
	}
	return snap, nil
}

// decodeLine decodes one line after the first and applies it to entries
// or values. A line is applied only if it decodes whole, so a torn line
// changes nothing.
func decodeLine(raw []byte, entries *[]session.Entry, values *replay) error {
	var head lineType
	if err := json.Unmarshal(raw, &head); err != nil {
		return err
	}

	// A skipped line would lose history, so a line of an unknown type
	// is an error.
	switch head.Type {
	case typeMessage:
		var l entryLine
		if err := json.Unmarshal(raw, &l); err != nil {
			return err
		}
		*entries = append(*entries, l.Entry)
		return nil
	case typeValue:
		var l valueLine
		if err := json.Unmarshal(raw, &l); err != nil {
			return err
		}
		return values.apply(l)
	default:
		return fmt.Errorf("unexpected line type %q", head.Type)
	}
}

// replay holds the values while Load reads the value lines in order.
type replay struct {
	order  []string
	byAddr map[string]session.StoredValue
}

func newReplay() *replay {
	return &replay{byAddr: make(map[string]session.StoredValue)}
}

// apply sets or deletes one value.
func (r *replay) apply(l valueLine) error {
	addr := l.Namespace + "\x00" + l.Key
	switch l.Op {
	case opSet:
		if _, ok := r.byAddr[addr]; !ok {
			r.order = append(r.order, addr)
		}
		r.byAddr[addr] = session.StoredValue{
			Namespace: l.Namespace,
			Key:       l.Key,
			Data:      l.Value,
		}
		return nil
	case opDelete:
		delete(r.byAddr, addr)
		return nil
	default:
		return fmt.Errorf("unexpected value op %q", l.Op)
	}
}

// current returns the values that are set, in the order of their first
// set.
func (r *replay) current() []session.StoredValue {
	var out []session.StoredValue
	for _, addr := range r.order {
		if v, ok := r.byAddr[addr]; ok {
			out = append(out, v)
		}
	}
	return out
}

// Append implements [session.Storer]. It writes one line per entry and
// never touches the first line.
func (s *SessionStore) Append(_ context.Context, id string, entries ...session.Entry) error {
	lines := make([]any, 0, len(entries))
	for i := range entries {
		lines = append(lines, entryLine{
			Type:  typeMessage,
			Entry: entries[i],
		})
	}
	return s.appendLines(id, lines...)
}

// SetValue implements [session.Storer]. It writes one value line.
func (s *SessionStore) SetValue(_ context.Context, id string, v session.StoredValue) error {
	return s.appendLines(id, valueLine{
		Type:      typeValue,
		Op:        opSet,
		Namespace: v.Namespace,
		Key:       v.Key,
		Value:     v.Data,
	})
}

// DeleteValue implements [session.Storer]. It writes one value line.
func (s *SessionStore) DeleteValue(_ context.Context, id, namespace, key string) error {
	return s.appendLines(id, valueLine{
		Type:      typeValue,
		Op:        opDelete,
		Namespace: namespace,
		Key:       key,
	})
}

// appendLines encodes each line as JSON and appends them to the file of
// id in one write. If the file does not exist, it returns
// [session.ErrNotFound]. If the write fails, the file does not change.
func (s *SessionStore) appendLines(id string, lines ...any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path, err := s.path(id)
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	for _, l := range lines {
		data, err := json.Marshal(l)
		if err != nil {
			return err
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, filePerm)
	if errors.Is(err, os.ErrNotExist) {
		return session.ErrNotFound
	}
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	// A short write leaves part of a line. The next append would join
	// it to a whole line, and Load rejects the joined line as
	// corruption. So the file goes back to its size before the write.
	if _, err := write(f, buf.Bytes()); err != nil {
		err = errors.Join(err, f.Truncate(info.Size()))
		f.Close()
		return err
	}
	return f.Close()
}

// readRecord decodes the first line, and returns the line as it read
// it. A first line with no newline that does not decode is torn, and
// readRecord returns [errTornRecord].
func readRecord(r *bufio.Reader, id string) (session.Session, []byte, error) {
	raw, err := r.ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return session.Session{}, nil, fmt.Errorf("fs: read %q: %w", id, err)
	}
	var l recordLine
	if err := json.Unmarshal(bytes.TrimSpace(raw), &l); err != nil {
		if !bytes.HasSuffix(raw, []byte{'\n'}) {
			return session.Session{}, nil, fmt.Errorf("fs: %q: %w", id, errTornRecord)
		}
		return session.Session{}, nil, fmt.Errorf("fs: decode session %q: %w", id, err)
	}
	if l.Type != typeSession {
		return session.Session{}, nil, fmt.Errorf("fs: %q does not start with a session_init line", id)
	}
	return l.Session, raw, nil
}

// endLastLine makes the file at path end with a newline, so that the
// next append starts a new line. The last line of the file is raw, with
// no newline, and whole is the size of the lines before it. A last
// line that decoded gets its newline. A torn one is cut.
func endLastLine(path string, whole int64, raw []byte, decoded bool) error {
	if len(raw) == 0 {
		return nil
	}
	if !decoded {
		return os.Truncate(path, whole)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, filePerm)
	if err != nil {
		return err
	}
	if _, err := f.Write([]byte{'\n'}); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// path returns the file of id, or an error if id is not a safe name.
func (s *SessionStore) path(id string) (string, error) {
	unsafe := id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\`)
	if unsafe {
		return "", fmt.Errorf("fs: invalid session id %q", id)
	}
	return filepath.Join(s.root, id+sessionExt), nil
}
