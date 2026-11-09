// Package fence runs a command inside an OS sandbox. A [Fence] limits
// the files that the command can write, the files that it can read, and
// its network. The child processes of the command stay inside the
// fence.
//
// [New] picks the backend of this OS. The backend of macOS is
// Seatbelt. [Workspace] is the policy for an agent that works in one
// project directory. [ProtectedNames] are the files that it keeps
// read-only, and [RealPath] and [Within] are the path rules of the
// fence, for a caller that checks a path as the fence does.
//
// A caller must obey these rules:
//
//   - A command can read every file that is not hidden. Give each secret
//     to [Hide].
//   - The fence matches real paths. It resolves the symlinks in each
//     path when it is built, so a path that becomes a symlink later is
//     not in the policy.
//   - [New] returns an error if the backend cannot run. A fence never
//     runs a command outside the sandbox.
//
// The package imports only the standard library, so a program without
// gopi can use it.
//
// The reasons are in docs/concepts/sandbox/fence.md.
package fence

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"unicode"
)

// Fence runs commands inside an OS sandbox.
type Fence interface {
	// Command returns a command that runs argv in dir inside the fence.
	// The caller sets its stdio and runs it.
	Command(ctx context.Context, dir string, argv ...string) (*exec.Cmd, error)
}

// Option configures a fence.
type Option func(*config)

// Write makes dirs and their subtrees writable. All other paths are
// read-only.
func Write(dirs ...string) Option {
	return func(c *config) { c.write = append(c.write, dirs...) }
}

// ReadOnly keeps paths read-only inside a writable directory. The
// command cannot create, change, or remove a path, or rename a
// directory above it, such as .git above .git/hooks.
func ReadOnly(paths ...string) Option {
	return func(c *config) { c.readOnly = append(c.readOnly, paths...) }
}

// Hide makes paths and their subtrees unreadable and unwritable.
func Hide(paths ...string) Option {
	return func(c *config) { c.hide = append(c.hide, paths...) }
}

// Network turns the network on or off. The network is off by default.
// A later Network replaces an earlier one.
func Network(on bool) Option {
	return func(c *config) { c.network = on }
}

// SetEnv gives the command the variable name with value. It replaces
// the variable with the same name in the environment of the caller.
func SetEnv(name, value string) Option {
	return func(c *config) { c.setEnv = append(c.setEnv, name+"="+value) }
}

// ReadOnlyNames keeps each path with one of names read-only, at root and
// in each directory under root. A name is a relative path, such as
// .bashrc or .git/hooks. The rule covers the subtree of the path, and
// it matches a name in any letter case. The command also cannot remove
// or rename a directory with the first part of a name, such as .git,
// because a rename moves the path out of the rule.
func ReadOnlyNames(root string, names ...string) Option {
	return func(c *config) {
		c.names = append(c.names, nameRule{root: root, names: names})
	}
}

// protectedNames are the files and directories that a program outside
// the fence loads and runs. The list joins the list of sandbox-runtime
// (https://github.com/anthropics/sandbox-runtime/blob/22b545aa6661491bcb911e7239a8d29b17de0bae/src/sandbox/sandbox-utils.ts)
// and the list of Claude Code 2.1.316, with .git/commondir added: git
// reads hooks and config from the directory that commondir names.
//
// The list names the parts of .git and .claude that run code, and not
// the whole folder, because a command writes the rest of them: git
// writes its objects in .git, and Claude Code keeps its worktrees in
// .claude/worktrees.
var protectedNames = []string{
	// The folders that git, editors, and package managers run files of.
	".git/hooks",
	".git/config",
	".git/commondir",
	".claude/commands",
	".claude/agents",
	".config/git",
	".vscode",
	".idea",
	".husky",
	".cargo",
	".devcontainer",
	".yarn",
	".mvn",
	// The files that a shell, git, or a package manager reads and runs.
	".gitconfig",
	".gitmodules",
	".bashrc",
	".bash_profile",
	".zshrc",
	".zprofile",
	".profile",
	".zshenv",
	".zlogin",
	".zlogout",
	".bash_login",
	".bash_aliases",
	".bash_logout",
	".envrc",
	".ripgreprc",
	".mcp.json",
	".claude.json",
	".npmrc",
	".yarnrc",
	".yarnrc.yml",
	".pnp.cjs",
	".pnp.loader.mjs",
	".pnpmfile.cjs",
	"bunfig.toml",
	".bunfig.toml",
	".bazelrc",
	".bazelversion",
	".bazeliskrc",
	".pre-commit-config.yaml",
	"lefthook.yml",
	".lefthook.yml",
	"lefthook.yaml",
	".lefthook.yaml",
	"gradle-wrapper.properties",
	"maven-wrapper.properties",
	".devcontainer.json",
	"pyrightconfig.json",
}

// ProtectedNames returns the files and directories that a program
// outside the fence loads and runs, such as .bashrc, .envrc, and
// .git/hooks. A name is a relative path in lower case. It matches at
// each depth and in any letter case, and it covers its subtree.
// [Workspace] keeps each of them read-only.
func ProtectedNames() []string {
	return slices.Clone(protectedNames)
}

// workspaceCache is the directory under the temp directory where the
// build tools of a [Workspace] command keep their caches. A program
// outside the fence does not read it.
const workspaceCache = "gopi-cache"

// Workspace returns the policy for a command that works in the project
// directory root:
//
//   - It can write to root and to the temp directory. Its build tools
//     keep their caches in the temp directory, because a program outside
//     the fence reads the cache directory of the user.
//   - It cannot change the files of [ProtectedNames], at root and under
//     it, such as .git/hooks, .git/config, .gitmodules, and .bashrc.
//     Git or a shell runs them outside the fence the next time the user
//     runs it. So git init fails in the fence: it creates .git/hooks.
//   - It cannot change .git/modules, which holds the hooks and the
//     config of each submodule.
//   - If root has no .git directory, it cannot create or change .git. A
//     .git file points git to another directory, which can hold a hook.
//   - It can read each path. To hide a path, add [Hide] after the policy.
//   - It has the network and the whole environment of the caller.
//
// The policy follows the sandbox of Claude Code: it limits the writes
// and leaves the reads open. A command reads its toolchain from the
// home directory, so a hidden home directory breaks builds.
//
// Each directory in more is a root too: the command can write to it and
// read it, with the same rules for git as root. For example, more can
// hold a directory of git clones, so the command can add a worktree of
// a clone in root.
//
// To change the policy, add options after it:
//
//	fence.New(append(fence.Workspace(root), fence.Network(false))...)
func Workspace(root string, more ...string) []Option {
	roots := append([]string{root}, more...)
	write := append(slices.Clone(roots), os.TempDir())
	cache := filepath.Join(os.TempDir(), workspaceCache)

	var opts []Option

	var readOnly []string
	for _, r := range roots {
		git := filepath.Join(r, ".git")
		readOnly = append(readOnly, filepath.Join(git, "modules"))
		if info, err := os.Stat(git); err != nil || !info.IsDir() {
			readOnly = append(readOnly, git)
		}
		opts = append(opts, ReadOnlyNames(r, protectedNames...))
	}

	return append(opts,
		Write(write...),
		ReadOnly(readOnly...),
		Network(true),
		SetEnv("XDG_CACHE_HOME", cache),
		SetEnv("GOCACHE", filepath.Join(cache, "go-build")),
		SetEnv("PIP_CACHE_DIR", filepath.Join(cache, "pip")),
	)
}

// New returns the fence of the backend for this OS. On macOS, the fence
// runs each command with sandbox-exec, and New runs a test command in
// the fence first. A command in it can use only a short list of system
// services, and a setuid program, such as ps, does not run. On another
// OS, New returns an error that wraps [errors.ErrUnsupported].
func New(opts ...Option) (Fence, error) {
	switch runtime.GOOS {
	case "darwin":
		return newSeatbelt(opts...)
	default:
		return nil, fmt.Errorf("fence: no backend for %s: %w", runtime.GOOS, errors.ErrUnsupported)
	}
}

// Rules are the file rules of a policy, for a caller that reads and
// writes files itself, outside a fenced command. A sandbox checks the
// path of each file tool against them, so that a tool cannot reach a
// path that a fenced command cannot reach.
type Rules struct {
	c config
}

// NewRules returns the file rules of the policy opts.
func NewRules(opts ...Option) Rules {
	return Rules{c: newConfig(opts...).resolved()}
}

// foldCase is true when a deny rule of [Rules] matches a path in any
// letter case. The default disk of macOS ignores case, so ~/.SSH opens
// the hidden ~/.ssh. Seatbelt matches the path on disk, so a fenced
// command cannot use another case. An allow rule matches only the exact
// case, so that a disk that respects case gets no extra writable path.
var foldCase = runtime.GOOS == "darwin"

// CanRead reports whether a fenced command can read the absolute path
// p: p is not hidden.
func (r Rules) CanRead(p string) bool {
	real, ok := rulePath(p)
	return ok && !r.hidden(real)
}

// CanWrite reports whether a fenced command can write the absolute path
// p: p is in a writable directory, and it is not read-only or hidden.
func (r Rules) CanWrite(p string) bool {
	real, ok := rulePath(p)
	if !ok {
		return false
	}
	writable := underAny(r.c.write, real, false)
	denied := underAny(r.c.readOnly, real, foldCase) || r.readOnlyName(real) || r.hidden(real)
	return writable && !denied
}

// hidden reports whether real is under a path of [Hide].
func (r Rules) hidden(real string) bool {
	return underAny(r.c.hide, real, foldCase)
}

// readOnlyName reports whether real matches a rule of [ReadOnlyNames].
func (r Rules) readOnlyName(real string) bool {
	for _, n := range r.c.names {
		for _, name := range n.names {
			if regexp.MustCompile(namePattern(n.root, name)).MatchString(real) {
				return true
			}
		}
	}
	return false
}

// rulePath returns the real path of p for a check of [Rules]. It
// refuses a path that does not resolve. macOS has aliases that
// EvalSymlinks does not resolve, but Seatbelt matches the path behind
// them. A path under /System/Volumes/Data is the same path without the
// prefix. A path under /.vol names a file by its inode, so rulePath
// refuses it.
func rulePath(p string) (string, bool) {
	real, err := RealPath(p)
	if err != nil {
		return "", false
	}
	if runtime.GOOS != "darwin" {
		return real, true
	}
	if Within(volPrefix, real) {
		return "", false
	}
	if rest, ok := strings.CutPrefix(real, dataVolumePrefix); ok {
		return "/" + rest, true
	}
	return real, true
}

// The aliases of a path on macOS. See rulePath.
const (
	volPrefix        = "/.vol"
	dataVolumePrefix = "/System/Volumes/Data/"
)

// namePattern returns the regular expression for name at root and in
// each directory under root, and for the subtree of each. Each letter of
// name matches in both cases. Go and Seatbelt read the same syntax.
func namePattern(root, name string) string {
	return "^" + regexp.QuoteMeta(root) + "(/.*)?/" + anyCase(name) + "(/.*)?$"
}

// firstPartPattern returns the regular expression for the directories
// with the first part of name, at root and in each directory under root.
func firstPartPattern(root, name string) string {
	first, _, _ := strings.Cut(name, "/")
	return "^" + regexp.QuoteMeta(root) + "(/.*)?/" + anyCase(first) + "$"
}

// anyCase returns s as a regular expression that matches each letter in
// both cases.
func anyCase(s string) string {
	var b strings.Builder
	for _, r := range regexp.QuoteMeta(s) {
		lower, upper := unicode.ToLower(r), unicode.ToUpper(r)
		if lower == upper {
			b.WriteRune(r)
			continue
		}
		b.WriteString("[" + string(lower) + string(upper) + "]")
	}
	return b.String()
}

// underAny reports whether p is one of dirs or is in the subtree of one.
// If fold is true, it ignores the letter case.
func underAny(dirs []string, p string, fold bool) bool {
	if fold {
		p = strings.ToLower(p)
	}
	for _, d := range dirs {
		if fold {
			d = strings.ToLower(d)
		}
		if Within(d, p) {
			return true
		}
	}
	return false
}

type config struct {
	write    []string
	readOnly []string
	names    []nameRule
	hide     []string
	network  bool
	setEnv   []string
}

// nameRule is one [ReadOnlyNames] option.
type nameRule struct {
	root  string
	names []string
}

func newConfig(opts ...Option) config {
	var c config
	for _, opt := range opts {
		opt(&c)
	}
	return c
}

// resolved returns c with each path absolute and without symlinks. The
// backends match real paths only.
func (c config) resolved() config {
	c.write = realPaths(c.write)
	c.readOnly = realPaths(c.readOnly)
	c.hide = realPaths(c.hide)

	names := make([]nameRule, 0, len(c.names))
	for _, n := range c.names {
		names = append(names, nameRule{root: realPaths([]string{n.root})[0], names: n.names})
	}
	c.names = names
	return c
}

// realPaths returns each path of paths absolute and without symlinks.
// A path that does not resolve stays as it is.
func realPaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		if real, err := RealPath(p); err == nil {
			p = real
		}
		out = append(out, p)
	}
	return out
}

// maxSymlinks is how many symlinks a path can go through. A path with
// more, or a symlink loop, does not resolve. The number comes from the
// limit of the Linux kernel.
const maxSymlinks = 40

// RealPath returns the absolute path p without symlinks. If a part of p
// does not exist, RealPath resolves the parent folder and adds the rest
// of p. A symlink to a path that does not exist resolves to that path,
// because a write through the symlink creates it. RealPath returns an
// error if a symlink loops, goes through more than 40 symlinks, or
// cannot be read.
func RealPath(p string) (string, error) {
	hops := 0
	return realPath(p, &hops)
}

// realPath is [RealPath]. hops counts the symlinks that it follows by
// hand.
func realPath(p string, hops *int) (string, error) {
	real, err := filepath.EvalSymlinks(p)
	if err == nil {
		return real, nil
	}
	missing := errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
	if !missing {
		return "", err
	}
	parent := filepath.Dir(p)
	if parent == p {
		return p, nil
	}
	realParent, err := realPath(parent, hops)
	if err != nil {
		return "", err
	}
	joined := filepath.Join(realParent, filepath.Base(p))
	target, err := os.Readlink(joined)
	if err != nil {
		return joined, nil
	}
	*hops++
	if *hops > maxSymlinks {
		return "", fmt.Errorf("fence: the path %q has too many symlinks", p)
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(realParent, target)
	}
	return realPath(target, hops)
}

// Within reports whether p is base or is in the subtree of base. It
// compares the paths as text, so the caller resolves their symlinks
// first, for example with [RealPath].
func Within(base, p string) bool {
	rel, err := filepath.Rel(base, p)
	if err != nil {
		return false
	}
	parent := ".." + string(filepath.Separator)
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, parent) && !filepath.IsAbs(rel))
}
