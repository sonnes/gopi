package permission

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/sonnes/gopi/pkg/sandbox/fence"
)

// maxLinkHops is how many symlinks a path can go through. A path with
// more hops, or a symlink loop, does not resolve. The number comes from
// the limit of the Linux kernel.
const maxLinkHops = 40

// macAliases are the folders that macOS shows at two paths. A path that
// starts with the key is the same path with the value. A root check
// compares the short form of each path, so /private/tmp/a is in the root
// /tmp.
var macAliases = [][2]string{
	{"/private/tmp", "/tmp"},
	{"/private/var", "/var"},
}

// spellingsOf returns the spellings of the absolute path abs: abs, the
// target of each symlink on the way, and the real path. A path that does
// not exist yet gets the real path of its nearest folder that exists,
// with the rest of abs added. ok is false if a symlink loops,
// goes more than maxLinkHops hops, or cannot be read.
func spellingsOf(abs string) (out []string, ok bool) {
	out = []string{abs}
	cur := abs
	for range maxLinkHops {
		next, isLink, err := nextSpelling(cur)
		loops := isLink && slices.Contains(out, next)
		if err != nil || loops {
			return out, false
		}
		if next == cur {
			return out, true
		}
		if !slices.Contains(out, next) {
			out = append(out, next)
		}
		cur = next
	}
	return out, false
}

// nextSpelling returns the target of p if p is a symlink. Otherwise, it
// returns the real path of p.
func nextSpelling(p string) (next string, isLink bool, err error) {
	info, err := os.Lstat(p)
	isLink = err == nil && info.Mode()&fs.ModeSymlink != 0
	if !isLink {
		next, err = fence.RealPath(p)
		return next, false, err
	}
	target, err := os.Readlink(p)
	if err != nil {
		return "", true, err
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(p), target)
	}
	return filepath.Clean(target), true, nil
}

// shortForm returns p with the folder of a macOS alias in its short
// form. On another OS, it returns p.
func shortForm(p string) string {
	if runtime.GOOS != "darwin" {
		return p
	}
	for _, a := range macAliases {
		if rest, ok := strings.CutPrefix(p, a[0]); ok && (rest == "" || rest[0] == '/') {
			return a[1] + rest
		}
	}
	return p
}

// forms returns p and its short form, without repeats.
func forms(p string) []string {
	short := shortForm(p)
	if short == p {
		return []string{p}
	}
	return []string{p, short}
}

// rootSet holds the roots of a policy, each in the short form of the
// path as given and of its real path.
type rootSet []string

// newRootSet returns the roots of dirs. A relative dir is relative to
// base.
func newRootSet(dirs []string, base string) rootSet {
	var out rootSet
	for _, d := range dirs {
		if !filepath.IsAbs(d) {
			d = filepath.Join(base, d)
		}
		d = filepath.Clean(d)
		real, err := fence.RealPath(d)
		if err != nil {
			real = d
		}
		for _, f := range []string{shortForm(d), shortForm(real)} {
			if !slices.Contains(out, f) {
				out = append(out, f)
			}
		}
	}
	return out
}

// contains reports if each spelling is in one of the roots.
func (r rootSet) contains(spellings []string) bool {
	for _, s := range spellings {
		short := shortForm(s)
		in := slices.ContainsFunc(r, func(root string) bool {
			return fence.Within(root, short)
		})
		if !in {
			return false
		}
	}
	return true
}

// protectedDirs are the folders that make each path in them protected,
// at each depth, on top of [fence.ProtectedNames]. The fence protects
// only the parts of them that run code, because a fenced command writes
// the rest. An edit tool has no reason to write in them. The list comes
// from Claude Code 2.1.316.
var protectedDirs = []string{
	".git",
	".claude",
}

// claudeWorktrees is the folder in .claude that is not protected,
// because Claude Code keeps its worktrees there.
const claudeWorktrees = "worktrees"

// isProtected reports if an edit of the path p needs a question in each
// mode except [Bypass]. The match ignores the letter case, because the
// default disk of macOS does.
func isProtected(p string) bool {
	parts := strings.Split(filepath.ToSlash(strings.ToLower(p)), "/")
	last := len(parts) - 1
	for i, part := range parts {
		if !slices.Contains(protectedDirs, part) {
			continue
		}
		inWorktrees := part == ".claude" && i < last && parts[i+1] == claudeWorktrees
		if !inWorktrees {
			return true
		}
	}
	for _, name := range fence.ProtectedNames() {
		if hasParts(parts, strings.Split(name, "/")) {
			return true
		}
	}
	return false
}

// hasParts reports if want is a run of consecutive parts of parts.
func hasParts(parts, want []string) bool {
	for i := range len(parts) - len(want) + 1 {
		if slices.Equal(parts[i:i+len(want)], want) {
			return true
		}
	}
	return false
}
