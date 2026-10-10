// Package search holds the parts that the grep and find tools share: the
// rg command line, the output of rg, and the walk of a directory when rg
// is not installed.
package search

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/sonnes/gopi/pkg/sandbox"
	"github.com/sonnes/gopi/pkg/sandbox/internal/host"
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

// Base returns the clean path that a search starts at. An empty path is
// the working directory. A clean path gives the same paths in the output
// of rg and of the walk. In a sandbox with host paths, Base checks that
// the sandbox allows the path.
func Base(box sandbox.Sandbox, p string) (string, error) {
	if p == "" {
		p = "."
	}
	p = path.Clean(p)
	if hp, ok := box.(host.Pather); ok {
		if _, err := hp.HostPath(p); err != nil {
			return "", err
		}
	}
	return p, nil
}

// RunRG runs rg in box with args on the search path base. For a
// directory, rg starts in base and searches ".". Then a glob with a
// slash matches the path from base, and RunRG puts base back before each
// path. Exec cannot start a command in a file, so for a file, RunRG runs
// rg again with base as its argument.
func RunRG(ctx context.Context, box sandbox.Sandbox, base string, args []string) (sandbox.Result, error) {
	res, err := box.Exec(ctx, sandbox.Command{Line: rgLine(args, "."), Dir: base})
	if err == nil {
		res.Stdout = []byte(rebase(string(res.Stdout), base))
		return res, nil
	}
	if ctx.Err() != nil {
		return res, err
	}
	return box.Exec(ctx, sandbox.Command{Line: rgLine(args, base)})
}

// rgLine returns the shell line that runs rg with args on path.
func rgLine(args []string, path string) string {
	quoted := make([]string, 0, len(args)+3)
	quoted = append(quoted, "rg")
	for _, arg := range args {
		quoted = append(quoted, quote(arg))
	}
	quoted = append(quoted, "--", quote(path))
	return strings.Join(quoted, " ")
}

// rebase changes each path in the output of rg from a path in base to a
// path that starts with base. rg puts "./" before each path, and prints
// "--" between groups of context lines.
func rebase(out, base string) string {
	prefix := ""
	if base != "." {
		prefix = strings.TrimSuffix(base, "/") + "/"
	}
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
// pattern, as [MatchGlob] matches it. A path is relative to base. If
// null is true, each line has a NUL after its path, as with rg --null,
// and FilterGlob changes the NUL to a colon.
//
// rg obeys .gitignore for a file that a --glob matches, but not for a
// directory that it matches: --glob '*' lists all of node_modules. So
// the tools filter the paths after rg, with no --glob.
func FilterGlob(out, base, pattern string, null bool) string {
	prefix := ""
	if base != "." {
		prefix = strings.TrimSuffix(base, "/") + "/"
	}

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

// quote makes s one word for a POSIX shell.
func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
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

// WalkFiles returns the files under the host directory root, in lexical
// order, as slash paths relative to root. It skips hidden files and
// hidden directories, and symlinks, as rg does. It also skips each path
// that hp does not let the tools read, such as a path that a policy
// hides.
func WalkFiles(hp host.Pather, root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// rg skips a directory that it cannot read. The root must open.
			if p != root && d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return err
		}
		if p == root || d.Type()&fs.ModeSymlink != 0 {
			return nil
		}

		hidden := strings.HasPrefix(d.Name(), ".")
		if _, err := hp.HostPath(p); err != nil {
			hidden = true
		}
		if hidden && d.IsDir() {
			return fs.SkipDir
		}
		if hidden || d.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	return files, err
}
