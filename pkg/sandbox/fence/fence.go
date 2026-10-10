// Package fence runs a command inside an OS sandbox. A [Fence] limits
// the files that the command can write, the files that it can read, its
// network, and its environment. The child processes of the command stay
// inside the fence.
//
// [New] picks the backend of this OS. [Seatbelt] is the macOS backend.
// [Workspace] is the policy for an agent that works in one project
// directory.
//
// A caller must obey these rules:
//
//   - A command can read every file that is not hidden. Give each secret
//     to [Hide].
//   - The fence matches real paths. It resolves the symlinks in each
//     path when it is built, so a path that becomes a symlink later is
//     not in the policy.
//   - A constructor returns an error if the backend cannot run. A fence
//     never runs a command outside the sandbox.
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
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
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

// Env gives the command only the environment variables with these
// names. Each Env adds names. Without Env, the command gets the whole
// environment of the caller.
func Env(names ...string) Option {
	return func(c *config) { c.env = append(c.env, names...) }
}

// SetEnv gives the command the variable name with value. It replaces a
// variable with the same name that Env keeps.
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

// HideExcept makes dir and its subtree unreadable and unwritable, except
// each path in keep and its subtree. A command can still read the
// metadata of a path in dir, such as its size, because a tool that
// resolves a kept path reads the metadata of each parent.
func HideExcept(dir string, keep ...string) Option {
	return func(c *config) {
		c.hideExcept = append(c.hideExcept, hideExceptRule{dir: dir, keep: keep})
	}
}

// workspaceEnv are the environment variables of a [Workspace] command.
// The API keys in the environment of the caller stay out of the
// command. A key in a file that the command can read is not protected.
var workspaceEnv = []string{"PATH", "HOME", "USER", "LANG", "TERM", "SHELL", "TMPDIR"}

// workspaceHide are the directories and files under the home directory
// that hold credentials.
var workspaceHide = []string{
	".ssh",
	".aws",
	".azure",
	".config/gcloud",
	".config/gh",
	".docker",
	".gnupg",
	".kube",
	".netrc",
	".npmrc",
	".pypirc",
	".git-credentials",
	"Library/Keychains",
}

// workspaceKeep are the paths under the home directory that a
// [Workspace] command can read. Compilers and package managers read
// their toolchains and modules from them. The rest of the home
// directory is hidden.
var workspaceKeep = []string{
	".gitconfig",
	".gitignore_global",
	".gitignore",
	".gitattributes",
	".config/git",
	"go",
	".cargo",
	".rustup",
	".npm",
	".nvm",
	".pyenv",
	".rbenv",
	".local/bin",
	".local/lib",
	".bun",
	".deno",
	".volta",
	".asdf",
}

// workspaceNames are the files and directories that a program outside
// the fence loads and runs, at the root of a [Workspace] and in each
// directory under it. The list comes from sandbox-runtime
// (https://github.com/anthropics/sandbox-runtime/blob/22b545aa6661491bcb911e7239a8d29b17de0bae/src/sandbox/sandbox-utils.ts),
// with .git/commondir added: git reads hooks and config from the
// directory that commondir names.
var workspaceNames = []string{
	".gitconfig",
	".gitmodules",
	".bashrc",
	".bash_profile",
	".zshrc",
	".zprofile",
	".profile",
	".ripgreprc",
	".mcp.json",
	".vscode",
	".idea",
	".claude/commands",
	".claude/agents",
	".git/hooks",
	".git/config",
	".git/commondir",
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
//   - It cannot change the files in workspaceNames, at root and under
//     it, such as .git/hooks, .git/config, .gitmodules, and .bashrc.
//     Git or a shell runs them outside the fence the next time the user
//     runs it. So git init fails in the fence: it creates .git/hooks.
//   - It cannot change .git/modules, which holds the hooks and the
//     config of each submodule.
//   - If root has no .git directory, it cannot create or change .git. A
//     .git file points git to another directory, which can hold a hook.
//   - It cannot read the home directory, except root and the tool
//     directories in workspaceKeep. It never reads the credentials in
//     workspaceHide.
//   - It has no network.
//   - It gets only a short list of environment variables.
//
// To change the policy, add options after it:
//
//	fence.New(append(fence.Workspace(root), fence.Network(true))...)
func Workspace(root string) []Option {
	write := []string{root, os.TempDir()}
	cache := filepath.Join(os.TempDir(), workspaceCache)

	var hide []string
	var opts []Option
	if home, err := os.UserHomeDir(); err == nil {
		for _, p := range workspaceHide {
			hide = append(hide, filepath.Join(home, p))
		}
		keep := []string{root}
		for _, p := range workspaceKeep {
			keep = append(keep, filepath.Join(home, p))
		}
		opts = append(opts, HideExcept(home, keep...))
	}

	git := filepath.Join(root, ".git")
	readOnly := []string{filepath.Join(git, "modules")}
	if info, err := os.Stat(git); err != nil || !info.IsDir() {
		readOnly = append(readOnly, git)
	}

	return append(opts,
		Write(write...),
		ReadOnly(readOnly...),
		ReadOnlyNames(root, workspaceNames...),
		Hide(hide...),
		Network(false),
		Env(workspaceEnv...),
		SetEnv("XDG_CACHE_HOME", cache),
		SetEnv("GOCACHE", filepath.Join(cache, "go-build")),
		SetEnv("PIP_CACHE_DIR", filepath.Join(cache, "pip")),
	)
}

// New returns the fence of the backend for this OS: [Seatbelt] on
// macOS. On another OS, New returns an error that wraps
// [errors.ErrUnsupported].
func New(opts ...Option) (Fence, error) {
	switch runtime.GOOS {
	case "darwin":
		return Seatbelt(opts...)
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

// hidden reports whether real is under a path of [Hide], or under the
// directory of a [HideExcept] and not under a path that it keeps.
func (r Rules) hidden(real string) bool {
	if underAny(r.c.hide, real, foldCase) {
		return true
	}
	for _, h := range r.c.hideExcept {
		if underAny([]string{h.dir}, real, foldCase) && !underAny(h.keep, real, false) {
			return true
		}
	}
	return false
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

// rulePath returns the real path of p for a check of [Rules]. macOS has
// aliases that EvalSymlinks does not resolve, but Seatbelt matches the
// path behind them. A path under /System/Volumes/Data is the same path
// without the prefix. A path under /.vol names a file by its inode, so
// rulePath refuses it.
func rulePath(p string) (string, bool) {
	real := realPath(p)
	if runtime.GOOS != "darwin" {
		return real, true
	}
	if within(volPrefix, real) {
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
		if within(d, p) {
			return true
		}
	}
	return false
}

type config struct {
	write      []string
	readOnly   []string
	names      []nameRule
	hide       []string
	hideExcept []hideExceptRule
	network    bool
	env        []string
	setEnv     []string
}

// nameRule is one [ReadOnlyNames] option.
type nameRule struct {
	root  string
	names []string
}

// hideExceptRule is one [HideExcept] option.
type hideExceptRule struct {
	dir  string
	keep []string
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

	hideExcept := make([]hideExceptRule, 0, len(c.hideExcept))
	for _, h := range c.hideExcept {
		hideExcept = append(hideExcept, hideExceptRule{
			dir:  realPaths([]string{h.dir})[0],
			keep: realPaths(h.keep),
		})
	}
	c.hideExcept = hideExcept
	return c
}

func realPaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		out = append(out, realPath(p))
	}
	return out
}

// maxSymlinks is how many symlinks realPath follows before it stops, so
// that a symlink loop ends.
const maxSymlinks = 40

// realPath resolves the symlinks on the longest part of p that exists.
// It adds the parts that do not exist without change. A symlink to a
// path that does not exist resolves to that path, because a write
// through the symlink creates it.
func realPath(p string) string {
	return realPathN(p, maxSymlinks)
}

func realPathN(p string, hops int) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	if target, err := os.Readlink(p); err == nil && hops > 0 {
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(p), target)
		}
		return realPathN(target, hops-1)
	}
	parent := filepath.Dir(p)
	if parent == p {
		return p
	}
	return filepath.Join(realPathN(parent, hops), filepath.Base(p))
}

// keepEnv returns the entries of env with a name in names. If names is
// empty, it returns env.
func keepEnv(env, names []string) []string {
	if len(names) == 0 {
		return env
	}
	keep := make(map[string]bool, len(names))
	for _, n := range names {
		keep[n] = true
	}

	var out []string
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if keep[name] {
			out = append(out, kv)
		}
	}
	return out
}
