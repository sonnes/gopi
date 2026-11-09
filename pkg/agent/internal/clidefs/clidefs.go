// Package clidefs gives the driver of a CLI a clean folder for the
// definition files that the CLI reads. The driver loads the
// [def.Source] of its config and writes the files in the folder.
//
// A caller must obey these rules:
//
//   - Call [Folder.Prepare] before each start of the CLI, so an edit of
//     a definition reaches the next process.
//   - Call [Folder.Remove] when the agent closes.
package clidefs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sonnes/gopi/pkg/def"
	storefs "github.com/sonnes/gopi/pkg/store/fs"
)

// rootName is the folder in the temporary directory that holds the
// folder of each session.
const rootName = "gopi-defs"

// dirPerm keeps the folder of a session private to the user.
const dirPerm = 0o700

// Load returns the definitions of src. A nil src gives an empty set.
func Load(ctx context.Context, src def.Source) (def.Set, error) {
	if src == nil {
		return def.Set{}, nil
	}
	return src.Load(ctx)
}

// CheckSkills returns an error for the first name that no skill of src
// has. The harness fails on such a name too. Without names, it loads
// nothing.
func CheckSkills(ctx context.Context, src def.Source, names []string) error {
	if len(names) == 0 {
		return nil
	}
	set, err := Load(ctx, src)
	if err != nil {
		return err
	}
	for _, name := range names {
		if _, ok := set.Skill(name); !ok {
			return fmt.Errorf("clidefs: the skill %q: no skill has this name", name)
		}
	}
	return nil
}

// Folder is the folder of the definition files of one session of a CLI.
type Folder struct {
	root    string
	session string
	spec    string
}

// New returns the folder of the session: <session> in the folder
// gopi-defs of [os.TempDir]. spec is the model spec of the session.
func New(session, spec string) *Folder {
	return &Folder{
		root:    filepath.Join(os.TempDir(), rootName),
		session: session,
		spec:    spec,
	}
}

// Prepare clears the folder of the session for set, so that a
// definition that went away leaves no file, and returns the path of the
// folder. The path is empty if set has no skill and no agent: the CLI
// then needs no folder, and the folder of an earlier start goes away.
func (f *Folder) Prepare(set def.Set) (string, error) {
	if len(set.Skills) == 0 && len(set.Agents) == 0 {
		return "", f.Remove()
	}
	if err := storefs.CheckName(f.session); err != nil {
		return "", fmt.Errorf("clidefs: the session: %w", err)
	}
	dir := filepath.Join(f.root, f.session)
	if err := os.RemoveAll(dir); err != nil {
		return "", fmt.Errorf("clidefs: clear the folder: %w", err)
	}
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return "", fmt.Errorf("clidefs: make the folder: %w", err)
	}
	return dir, nil
}

// Remove removes the folder of the session. A folder that does not
// exist is not an error.
func (f *Folder) Remove() error {
	if storefs.CheckName(f.session) != nil {
		return nil
	}
	if err := os.RemoveAll(filepath.Join(f.root, f.session)); err != nil {
		return fmt.Errorf("clidefs: remove the folder: %w", err)
	}
	return nil
}

// Model returns the model of a definition for the CLI of the parent
// spec. A spec of the account of the parent, such as
// "claude-code/claude-sonnet-5-5", gives its model ID. The CLI runs on
// one account, so another spec, a bare model, and "inherit" give "", and
// the definition runs the model of the parent.
func (f *Folder) Model(model string) string {
	account, id, ok := strings.Cut(model, "/")
	own, _, _ := strings.Cut(f.spec, "/")
	if !ok || account != own {
		return ""
	}
	return id
}
