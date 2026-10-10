package fence

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

var (
	//go:embed base.sb
	seatbeltBase string
	//go:embed network.sb
	seatbeltNetwork string
	//go:embed preferences.sb
	seatbeltPreferences string
)

// seatbeltPath is the sandbox-exec binary. The fence uses only the copy
// in /usr/bin. A sandbox-exec earlier on PATH can be a program that runs
// the command without a sandbox.
var seatbeltPath = "/usr/bin/sandbox-exec"

// probeTimeout is how long [Seatbelt] waits for its test command.
const probeTimeout = 10 * time.Second

// seatbeltRules are the rules that come after the base profile and the
// write rules. A later rule wins in a Seatbelt profile.
var seatbeltRules = []string{
	// A write to /dev/stdout, /dev/stderr, or /dev/fd/N reuses a file
	// descriptor that the command has already. /dev/tty stays closed, so
	// a command cannot write to the terminal of the user.
	`(allow file-write* (regex #"^/dev/fd/[0-9]+$"))`,
	// An XPC service runs outside the sandbox of its caller.
	`(deny mach-lookup (xpc-service-name-prefix ""))`,
	// F_MAKECOMPRESSED and F_TRANSFEREXTENTS change a file through a
	// read-only descriptor.
	`(deny system-fcntl (fcntl-command 80 110))`,
}

type seatbelt struct {
	// args are the arguments of sandbox-exec before the command.
	args []string
	// env are the names of the environment variables that the command
	// gets. If env is empty, the command gets the whole environment.
	env []string
	// setEnv are the name=value variables that the command gets on top
	// of env.
	setEnv []string
}

// Seatbelt returns a fence that runs each command with sandbox-exec, the
// sandbox of macOS. Seatbelt runs a test command in the fence first. If
// the test fails, for example on another OS, Seatbelt returns an error.
//
// A command can read all files except the hidden ones, and it can use
// only a short list of system services. A setuid program, such as ps,
// does not run in the fence.
func Seatbelt(opts ...Option) (Fence, error) {
	c := newConfig(opts...).resolved()
	if err := checkPatternRoots(c); err != nil {
		return nil, err
	}
	s := &seatbelt{
		args:   seatbeltArgs(c),
		env:    c.env,
		setEnv: c.setEnv,
	}
	if err := s.probe(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *seatbelt) Command(ctx context.Context, dir string, argv ...string) (*exec.Cmd, error) {
	if len(argv) == 0 {
		return nil, errors.New("fence: the command is empty")
	}

	args := append(slices.Clone(s.args), argv...)
	cmd := exec.CommandContext(ctx, seatbeltPath, args...)
	cmd.Dir = dir
	if len(s.env) > 0 || len(s.setEnv) > 0 {
		// exec.Cmd uses the last value of a name, so setEnv wins.
		cmd.Env = append(keepEnv(os.Environ(), s.env), s.setEnv...)
	}
	return cmd, nil
}

// checkPatternRoots returns an error for a root of [ReadOnlyNames] that
// a regular expression of the profile cannot hold. A regular expression
// takes no parameter, so the root is text in the profile, and a quote or
// a newline ends the expression.
func checkPatternRoots(c config) error {
	for _, n := range c.names {
		if strings.ContainsAny(n.root, "\"\n") {
			return fmt.Errorf("fence: the path %q has a quote or a newline", n.root)
		}
	}
	return nil
}

// probe runs true in the fence. It fails if sandbox-exec is missing or
// rejects the profile.
func (s *seatbelt) probe() error {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()

	cmd, err := s.Command(ctx, "/", "/usr/bin/true")
	if err != nil {
		return err
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		return fmt.Errorf("fence: %s cannot run a command: %w: %s", seatbeltPath, err, msg)
	}
	return nil
}

// seatbeltArgs returns the arguments of sandbox-exec for c, up to the
// command: the profile, then a parameter for each path. A parameter
// keeps a path out of the profile text, so a path needs no quoting.
func seatbeltArgs(c config) []string {
	var params []string
	param := func(name, value string) string {
		params = append(params, "-D"+name+"="+value)
		return name
	}

	rules := []string{
		seatbeltBase,
		"(allow file-read*)",
		seatbeltPreferences,
	}

	if len(c.write) > 0 {
		var filters []string
		for i, dir := range c.write {
			p := param(fmt.Sprintf("WRITE_%d", i), dir)
			filters = append(filters, fmt.Sprintf(`(subpath (param "%s"))`, p))
		}
		rules = append(rules, "(allow file-write* "+strings.Join(filters, " ")+")")
	}
	if c.network {
		rules = append(rules, seatbeltNetwork)
	}
	rules = append(rules, seatbeltRules...)

	for i, path := range c.readOnly {
		p := param(fmt.Sprintf("READONLY_%d", i), path)
		// The literal rule also denies the first creation of the path.
		rules = append(rules, fmt.Sprintf(
			`(deny file-write* (literal (param "%[1]s")) (subpath (param "%[1]s")))`,
			p,
		))
		// A rename of a parent would move the path out of the rule.
		for j, parent := range readOnlyParents(c.write, path) {
			pp := param(fmt.Sprintf("READONLY_%d_PARENT_%d", i, j), parent)
			rules = append(rules, fmt.Sprintf(`(deny file-write-unlink (literal (param "%s")))`, pp))
		}
	}

	for _, n := range c.names {
		for _, name := range n.names {
			rules = append(rules, fmt.Sprintf(`(deny file-write* (regex #"%s"))`, namePattern(n.root, name)))
			if strings.Contains(name, "/") {
				// A rename of the first directory, such as .git, would move
				// the path out of the rule.
				rules = append(rules, fmt.Sprintf(`(deny file-write-unlink (regex #"%s"))`, firstPartPattern(n.root, name)))
			}
		}
	}

	for i, h := range c.hideExcept {
		p := param(fmt.Sprintf("HIDEEXCEPT_%d", i), h.dir)
		filters := []string{fmt.Sprintf(`(subpath (param "%s"))`, p)}
		for j, keep := range h.keep {
			k := param(fmt.Sprintf("HIDEEXCEPT_%d_KEEP_%d", i, j), keep)
			filters = append(filters, fmt.Sprintf(`(require-not (subpath (param "%s")))`, k))
		}
		// The metadata stays readable. A tool that resolves a kept path
		// reads the metadata of each parent.
		rules = append(rules, fmt.Sprintf(
			"(deny file-read-data file-read-xattr file-write* (require-all %s))",
			strings.Join(filters, " "),
		))
	}

	for i, path := range c.hide {
		p := param(fmt.Sprintf("HIDE_%d", i), path)
		rules = append(rules, fmt.Sprintf(
			`(deny file-read* file-write* (literal (param "%[1]s")) (subpath (param "%[1]s")))`,
			p,
		))
	}

	args := []string{"-p", strings.Join(rules, "\n")}
	args = append(args, params...)
	return append(args, "--")
}

// readOnlyParents returns the parents of path that a command can
// rename: each parent inside a write directory. A write directory can
// be inside another one, such as a root in the temp directory, so the
// command can rename the inner one too. The nearest parent comes first.
func readOnlyParents(write []string, path string) []string {
	renamable := func(p string) bool {
		for _, w := range write {
			if w != p && within(w, p) {
				return true
			}
		}
		return false
	}

	var parents []string
	for p := filepath.Dir(path); p != filepath.Dir(p); p = filepath.Dir(p) {
		if !renamable(p) {
			break
		}
		parents = append(parents, p)
	}
	return parents
}

// within reports whether p is base or is in the subtree of base.
func within(base, p string) bool {
	rel, err := filepath.Rel(base, p)
	if err != nil {
		return false
	}
	parent := ".." + string(filepath.Separator)
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, parent))
}
