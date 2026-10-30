// Package clidefs gives the definitions of a list of folders to the
// driver of a CLI. It merges the folders with [def.Resolve], as the
// harness does, and it gives the driver a clean folder for the files
// that the CLI reads.
//
// A caller must obey these rules:
//
//   - Call [Defs.Prepare] before each start of the CLI, so an edit of a
//     folder reaches the next process.
//   - Call [Defs.Remove] when the agent closes.
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
// folder of each session, if the driver has no root.
const rootName = "gopi-defs"

// dirPerm keeps the folder of a session private to the user.
const dirPerm = 0o700

// Defs is the definitions of one session of a CLI.
type Defs struct {
	dirs    []string
	root    string
	session string
	spec    string
}

// New returns the definitions of dirs, lowest first. The folder of the
// session is <root>/<session>. If root is empty, the root is the folder
// gopi-defs in [os.TempDir]. spec is the model spec of the session.
func New(dirs []string, root, session, spec string) *Defs {
	if root == "" {
		root = filepath.Join(os.TempDir(), rootName)
	}
	return &Defs{
		dirs:    dirs,
		root:    root,
		session: session,
		spec:    spec,
	}
}

// Check returns an error for the first name that no skill has. The
// harness fails on such a name too. Without names, it reads nothing.
func (d *Defs) Check(ctx context.Context, names []string) error {
	if len(names) == 0 {
		return nil
	}
	set, err := d.resolve(ctx)
	if err != nil {
		return err
	}
	for _, name := range names {
		if !set.HasSkill(name) {
			return fmt.Errorf("clidefs: the skill %q: no skill has this name", name)
		}
	}
	return nil
}

// Prepare merges the folders and clears the folder of the session, so
// that a definition that went away leaves no file. It returns the
// merged set and the path of the folder. The path is empty if the set
// is empty: the CLI then needs no folder.
func (d *Defs) Prepare(ctx context.Context) (def.Set, string, error) {
	set, err := d.resolve(ctx)
	if err != nil {
		return def.Set{}, "", err
	}
	if len(set.Skills) == 0 && len(set.Agents) == 0 {
		return set, "", nil
	}

	if !d.validSession() {
		return def.Set{}, "", fmt.Errorf("clidefs: the session %q is not a folder name", d.session)
	}
	dir := filepath.Join(d.root, d.session)
	if err := os.RemoveAll(dir); err != nil {
		return def.Set{}, "", fmt.Errorf("clidefs: clear the folder: %w", err)
	}
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return def.Set{}, "", fmt.Errorf("clidefs: make the folder: %w", err)
	}
	return set, dir, nil
}

// Remove removes the folder of the session. A folder that does not
// exist is not an error.
func (d *Defs) Remove() error {
	if !d.validSession() {
		return nil
	}
	if err := os.RemoveAll(filepath.Join(d.root, d.session)); err != nil {
		return fmt.Errorf("clidefs: remove the folder: %w", err)
	}
	return nil
}

// validSession reports whether the folder of the session stays in the
// root.
func (d *Defs) validSession() bool {
	return OneElement(d.session)
}

// OneElement reports whether name is one element of a path, so a file
// with this name stays in its folder. A name can come from a file of a
// folder that a person can clone.
func OneElement(name string) bool {
	return filepath.IsLocal(name) && filepath.Base(name) == name
}

// resolve merges the folders. A folder that does not exist gives
// nothing.
func (d *Defs) resolve(ctx context.Context) (def.Set, error) {
	readers := make([]def.Reader, len(d.dirs))
	for i, dir := range d.dirs {
		readers[i] = storefs.Dir(dir)
	}
	return def.Resolve(ctx, readers...)
}

// Model returns the model of a definition for the CLI of the parent
// spec. A spec of the account of the parent, such as
// "claude-code/claude-sonnet-5-5", gives its model ID. The CLI runs on
// one account, so another spec, a bare model, and "inherit" give "", and
// the definition runs the model of the parent.
func (d *Defs) Model(model string) string {
	account, id, ok := strings.Cut(model, "/")
	own, _, _ := strings.Cut(d.spec, "/")
	if !ok || account != own {
		return ""
	}
	return id
}
