package chatgpt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/sonnes/gopi/pkg/ai/oauth"
)

// StoreKey is the key of the account in a login file. A login file is a
// JSON object, and the other keys belong to other providers.
const StoreKey = "chatgpt"

// The modes of the login file and of its folder. The file holds tokens,
// so only the user can read it.
const (
	fileMode = 0o600
	dirMode  = 0o700
)

// lockSuffix ends the name of the lock file of a login file.
const lockSuffix = ".lock"

// LoadAccount reads the account of the login file at path. If the file
// or the account does not exist, the error wraps [ErrNoAccount].
func LoadAccount(path string) (Account, error) {
	file, err := readFile(path)
	if err != nil {
		return Account{}, err
	}
	raw, ok := file[StoreKey]
	if !ok {
		return Account{}, ErrNoAccount
	}
	var a Account
	if err := json.Unmarshal(raw, &a); err != nil {
		return Account{}, fmt.Errorf("chatgpt: read %s: %w", path, err)
	}
	return a, nil
}

// SaveAccount writes a to the login file at path, with mode 0600. It
// keeps the other keys of the file. It writes a temporary file and
// renames it, so a reader never sees half a file.
func SaveAccount(path string, a Account) error {
	file, err := readFile(path)
	if err != nil && !errors.Is(err, ErrNoAccount) {
		return err
	}
	if file == nil {
		file = map[string]json.RawMessage{}
	}
	raw, err := json.Marshal(a)
	if err != nil {
		return fmt.Errorf("chatgpt: write %s: %w", path, err)
	}
	file[StoreKey] = raw
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("chatgpt: write %s: %w", path, err)
	}

	if err := os.MkdirAll(filepath.Dir(path), dirMode); err != nil {
		return fmt.Errorf("chatgpt: write %s: %w", path, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("chatgpt: write %s: %w", path, err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(fileMode); err != nil {
		tmp.Close()
		return fmt.Errorf("chatgpt: write %s: %w", path, err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("chatgpt: write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("chatgpt: write %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("chatgpt: write %s: %w", path, err)
	}
	return nil
}

// readFile reads the keys of the login file at path. If the file does
// not exist, the error wraps [ErrNoAccount].
func readFile(path string) (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoAccount
	}
	if err != nil {
		return nil, fmt.Errorf("chatgpt: read %s: %w", path, err)
	}
	var file map[string]json.RawMessage
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("chatgpt: read %s: %w", path, err)
	}
	return file, nil
}

// FileSource returns an [oauth.Source] on the login file at path. It
// keeps the token in memory while the token is valid. To refresh, it
// locks the file, reads the file again, and refreshes only if the token
// in the file is still not valid. Another process can have refreshed
// it. Then it writes the new tokens to the file.
//
// If the file has no signed-in account, Token returns an error that
// wraps [ErrNoAccount].
func FileSource(path string, auth Auth) oauth.Source {
	return &fileSource{
		path: path,
		auth: auth,
	}
}

type fileSource struct {
	path string
	auth Auth

	mu     sync.Mutex
	cached oauth.Token
}

func (s *fileSource) Token(ctx context.Context) (oauth.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cached.Valid() {
		return s.cached, nil
	}

	unlock, err := lockFile(s.path + lockSuffix)
	if err != nil {
		return oauth.Token{}, fmt.Errorf("chatgpt: lock %s: %w", s.path, err)
	}
	defer unlock()

	a, err := LoadAccount(s.path)
	if err != nil {
		return oauth.Token{}, err
	}
	if !a.SignedIn() {
		return oauth.Token{}, ErrNoAccount
	}
	if !a.Token.Valid() {
		if a, err = s.auth.Refresh(ctx, a); err != nil {
			return oauth.Token{}, err
		}
		if err := SaveAccount(s.path, a); err != nil {
			return oauth.Token{}, err
		}
	}
	s.cached = a.Token
	return a.Token, nil
}
