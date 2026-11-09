// Package search holds the parts that the grep and find tools share: the
// root of a search, the rg command line, the output of rg, and the walk
// of a directory when rg is not installed.
package search

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/sonnes/gopi/pkg/sandbox"
	"github.com/sonnes/gopi/pkg/sandbox/internal/host"
	"github.com/sonnes/gopi/pkg/sandbox/internal/output"
)

// ExitNotFound is the exit code of a POSIX shell when it cannot find
// the command.
const ExitNotFound = 127

// SkipHidden is an rg glob that skips hidden files and directories. A
// glob that the model gives, such as "*", also matches hidden names, and
// rg then includes them. A later glob wins, so SkipHidden goes last.
const SkipHidden = "!.*"

// ErrNoRG is the error of a search in a remote sandbox without rg.
var ErrNoRG = errors.New("rg is not installed in the sandbox: install ripgrep in its image")

// Limit returns the limit that the model gave, or def if the model gave
// no limit or a limit below 1.
func Limit(limit *int, def int) int {
	if limit != nil && *limit > 0 {
		return *limit
	}
	return def
}

// Root is the path that a search starts at.
type Root struct {
	// Path is the clean path of the model. A clean path gives the same
	// paths in the output of rg and of the walk.
	Path string

	box sandbox.Sandbox
	// hp and host are the sandbox and the host path of Path, in a
	// sandbox with host paths.
	hp   host.Pather
	host string
}

// NewRoot returns the root of a search at p in box. An empty p is the
// working directory. In a sandbox with host paths, NewRoot checks that
// the sandbox allows p.
func NewRoot(box sandbox.Sandbox, p string) (Root, error) {
	if p == "" {
		p = "."
	}
	r := Root{
		Path: path.Clean(p),
		box:  box,
	}
	if hp, ok := box.(host.Pather); ok {
		hostPath, err := hp.HostPath(r.Path)
		if err != nil {
			return Root{}, err
		}
		r.hp = hp
		r.host = hostPath
	}
	return r, nil
}

// RunRG runs rg in the sandbox with args on the root. For a directory,
// rg starts in the root and searches ".". Then a glob with a slash
// matches the path from the root, and RunRG puts the root back before
// each path. Exec cannot start a command in a file, so for a file, RunRG
// runs rg again with the root as its argument.
func (r Root) RunRG(ctx context.Context, args []string) (sandbox.Result, error) {
	res, err := r.box.Exec(ctx, sandbox.Command{Line: rgLine(args, "."), Dir: r.Path})
	if err == nil {
		res.Stdout = []byte(rebase(string(res.Stdout), r.prefix()))
		return res, nil
	}
	if ctx.Err() != nil {
		return res, err
	}
	return r.box.Exec(ctx, sandbox.Command{Line: rgLine(args, r.Path)})
}

// rgLine returns the shell line that runs rg with args on path.
func rgLine(args []string, path string) string {
	quoted := make([]string, 0, len(args)+3)
	quoted = append(quoted, "rg")
	for _, arg := range args {
		quoted = append(quoted, output.Quote(arg))
	}
	quoted = append(quoted, "--", output.Quote(path))
	return strings.Join(quoted, " ")
}

// prefix returns the text before each path under the root: the path of
// the root and a slash, or "" for the working directory.
func (r Root) prefix() string {
	if r.Path == "." {
		return ""
	}
	return strings.TrimSuffix(r.Path, "/") + "/"
}

// rebase changes each path in the output of rg from a path under the
// root to a path that starts with prefix. rg puts "./" before each path,
// and prints "--" between groups of context lines.
func rebase(out, prefix string) string {
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if line == "" || line == "--" {
			continue
		}
		lines[i] = prefix + strings.TrimPrefix(line, "./")
	}
	return strings.Join(lines, "\n")
}

// Output returns the output of an rg run that ended with a match or
// with no match. rg ends with exit code 2 when it cannot read a file,
// and it still prints the matches in the other files. Then Output
// keeps the matches and adds the first error. For exit code 2 with no
// output, ok is false.
//
// A fence denies a hidden path with "Operation not permitted". Output
// drops that error, so the model does not learn that the path exists.
func Output(res sandbox.Result, limit int) (out string, ok bool) {
	out = CapLines(string(res.Stdout), limit)
	msg := firstError(string(res.Stderr))
	switch {
	case res.ExitCode == 0 || res.ExitCode == 1:
		return out, true
	case res.ExitCode == 2 && out != "" && msg == "":
		return out, true
	case res.ExitCode == 2 && out != "":
		return out + "\n... (rg could not search some files: " + msg + ")", true
	default:
		return "", false
	}
}

// fenceDenied is the end of an rg error for a path that a fence denies.
const fenceDenied = "Operation not permitted (os error 1)"

// firstError returns the first line of the rg errors that is not a
// denial of the fence.
func firstError(stderr string) string {
	for line := range strings.Lines(stderr) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasSuffix(line, fenceDenied) {
			continue
		}
		return line
	}
	return ""
}

// FilterGlob keeps the lines of the rg output out whose path matches
// pattern, as [MatchGlob] matches it. A path is relative to the root. If
// null is true, each line has a NUL after its path, as with rg --null,
// and FilterGlob changes the NUL to a colon.
//
// rg obeys .gitignore for a file that a --glob matches, but not for a
// directory that it matches: --glob '*' lists all of node_modules. So
// the tools filter the paths after rg, with no --glob.
func (r Root) FilterGlob(out, pattern string, null bool) string {
	prefix := r.prefix()
	var kept []string
	for line := range strings.Lines(out) {
		line = strings.TrimSuffix(line, "\n")
		p, rest, hasRest := line, "", false
		if null {
			p, rest, hasRest = strings.Cut(line, "\x00")
		}
		if !MatchGlob(pattern, strings.TrimPrefix(p, prefix)) {
			continue
		}
		if hasRest {
			line = p + ":" + rest
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// Error returns the error of an rg run that did not end with a match
// or with no match.
func Error(res sandbox.Result) error {
	msg := strings.TrimSpace(string(res.Stderr))
	if msg == "" {
		msg = fmt.Sprintf("rg ended with exit code %d", res.ExitCode)
	}
	return errors.New(msg)
}

// CapLines returns at most limit lines of out, with a note when it
// removes lines. If out is empty, CapLines returns empty.
func CapLines(out string, limit int) string {
	out = strings.TrimRight(out, "\n")
	if out == "" {
		return ""
	}
	lines := strings.Split(out, "\n")
	if len(lines) <= limit {
		return out
	}
	return strings.Join(lines[:limit], "\n") + truncatedNote(limit)
}

func truncatedNote(limit int) string {
	return fmt.Sprintf("\n... (results truncated to %d)", limit)
}

// MatchGlob reports whether the relative path rel matches pattern. A
// pattern without a slash matches the file name at any depth, as in rg.
// A pattern with a slash matches the full relative path.
func MatchGlob(pattern, rel string) bool {
	name := rel
	if !strings.Contains(pattern, "/") {
		name = path.Base(rel)
	}
	ok, _ := doublestar.Match(pattern, name)
	return ok
}

// File is a file that [Root.Walk] finds.
type File struct {
	// Path is the path of the file for [sandbox.Sandbox.ReadFile]: the
	// path of the root, then the path under it.
	Path string
	// Rel is the slash path of the file under the root. For a root that
	// is a file, Rel is the name of the file.
	Rel string
	// ModTime is the time of the last change of the file.
	ModTime time.Time
}

// Walk returns the regular files under the root, in lexical order. It
// skips hidden files and hidden directories, symlinks, and each file
// that is not a regular file, such as a FIFO, as rg does. It also skips
// each path that the sandbox does not let the tools read, such as a
// path that a policy hides. For a root that is a regular file, Walk
// returns the file alone. In a sandbox without host paths, Walk returns
// [ErrNoRG], because it walks only a directory of this host.
func (r Root) Walk() ([]File, error) {
	if r.hp == nil {
		return nil, ErrNoRG
	}
	info, err := os.Stat(r.host)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		if !info.Mode().IsRegular() {
			return nil, nil
		}
		return []File{{
			Path:    r.Path,
			Rel:     filepath.Base(r.host),
			ModTime: info.ModTime(),
		}}, nil
	}

	var files []File
	err = filepath.WalkDir(r.host, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// rg skips a directory that it cannot read. The root must open.
			if p != r.host && d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return err
		}
		if p == r.host {
			return nil
		}

		hidden := strings.HasPrefix(d.Name(), ".")
		if _, err := r.hp.HostPath(p); err != nil {
			hidden = true
		}
		if hidden && d.IsDir() {
			return fs.SkipDir
		}
		if hidden || !d.Type().IsRegular() {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(r.host, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		files = append(files, File{
			Path:    path.Join(r.Path, rel),
			Rel:     rel,
			ModTime: info.ModTime(),
		})
		return nil
	})
	return files, err
}
