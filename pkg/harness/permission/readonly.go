package permission

import (
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// This file finds the bash commands that only read. The design comes
// from the read-only commands of Claude Code 2.1.316: a list of
// commands, the flags of each command that only read, and a path check
// for each path argument. The parser is stricter than a shell. If it
// cannot tell what a command does, the command is not read-only, and
// the mode decides it as any other command.

// argKind is what a flag of a command takes.
type argKind int

const (
	// noArg is a flag with no value, such as -l.
	noArg argKind = iota
	// valueArg is a flag with a value that is not a path, such as -n 5.
	valueArg
	// pathArg is a flag with a value that is a path that the command
	// reads, such as grep -f patterns.txt.
	pathArg
	// optionalArg is a long flag with no value, or with a value after
	// "=", such as --color or --color=never.
	optionalArg
)

// operands is what the operands of a command are. An operand is an
// argument that is not a flag or the value of a flag.
type operands int

const (
	// pathOperands are paths that the command reads.
	pathOperands operands = iota
	// patternThenPaths is a pattern, then paths, as in grep. A flag
	// that gives the pattern, such as -e, makes each operand a path.
	patternThenPaths
	// textOperands are text that the command does not open, as in echo.
	textOperands
	// noOperands is a command that takes no operand.
	noOperands
)

// commandSpec is what the parser knows about one read-only command.
type commandSpec struct {
	// flags are the flags that only read. Each other flag makes the
	// command not read-only.
	flags flagSet
	// operands is what the operands are.
	operands operands
	// maxOperands is the most operands, or 0 for no limit.
	maxOperands int
	// numberFlag is true if a flag such as -5 is a count, as in head.
	numberFlag bool
	// patternFlags give the pattern, so that each operand is a path.
	patternFlags []string
}

// flagSet maps each flag of a command to what it takes.
type flagSet map[string]argKind

// flags returns a flag set with each name of kind k.
func flags(k argKind, names ...string) flagSet {
	return flagSet{}.with(k, names...)
}

// with returns a copy of f with each name of kind k added.
func (f flagSet) with(k argKind, names ...string) flagSet {
	out := make(flagSet, len(f)+len(names))
	maps.Copy(out, f)
	for _, n := range names {
		out[n] = k
	}
	return out
}

// grepFlags are the flags of grep that only read. -R is not in the
// list, because it follows each symlink out of the roots.
var grepFlags = flags(noArg,
	"-i", "-v", "-n", "-c", "-l", "-L", "-r", "-h", "-H", "-w", "-x",
	"-o", "-s", "-q", "-E", "-F", "-G", "-P", "-z", "-Z", "-I", "-a",
	"--ignore-case", "--invert-match", "--line-number", "--count",
	"--files-with-matches", "--files-without-match", "--recursive",
	"--no-filename", "--with-filename", "--word-regexp", "--line-regexp",
	"--only-matching", "--no-messages", "--quiet", "--silent",
	"--extended-regexp", "--fixed-strings", "--basic-regexp",
	"--perl-regexp", "--null",
).with(valueArg, "-A", "-B", "-C", "-m", "-e", "--after-context", "--before-context", "--context", "--max-count", "--regexp", "--include", "--exclude", "--exclude-dir", "--binary-files").with(optionalArg, "--color", "--colour").with(pathArg, "-f", "--file")

// rgFlags are the flags of rg that only read. --pre and -z run another
// program, and -L follows each symlink out of the roots.
var rgFlags = flags(noArg,
	"-i", "-S", "-s", "-v", "-n", "-N", "-c", "-l", "-w", "-x", "-o",
	"-F", "-U", "-u", "--hidden", "--no-ignore", "--no-ignore-vcs",
	"--ignore-case", "--smart-case", "--case-sensitive", "--invert-match",
	"--line-number", "--no-line-number", "--count", "--files-with-matches",
	"--word-regexp", "--line-regexp", "--only-matching", "--fixed-strings",
	"--multiline", "--files", "--json", "--no-heading", "--heading",
	"--vimgrep", "--trim", "--stats", "--no-messages", "--null",
).with(valueArg, "-g", "--glob", "--iglob", "-t", "--type", "-T", "--type-not", "-A", "-B", "-C", "-m", "-e", "--after-context", "--before-context", "--context", "--max-count", "--regexp", "--max-depth", "-d", "--sort", "--sortr", "--color", "--max-columns", "-M", "--type-add", "-r", "--replace").with(pathArg, "-f", "--file")

// readOnlyCommands are the commands that the parser knows. A command
// that is not here is not read-only.
var readOnlyCommands = map[string]commandSpec{
	"ls": {
		flags: flags(noArg,
			"-a", "-A", "-l", "-h", "-1", "-R", "-t", "-r", "-S", "-d", "-F",
			"-G", "-i", "-s", "-n", "-p", "-C", "-m", "-x", "-T", "-U", "-c",
			"-u", "-o", "-g", "-k", "-@", "-e", "-O",
			"--all", "--almost-all", "--human-readable", "--recursive",
			"--classify", "--directory", "--reverse", "--size",
			"--inode", "--numeric-uid-gid", "--group-directories-first",
		).with(optionalArg, "--color"),
	},
	"cat": {
		flags: flags(noArg, "-n", "-b", "-A", "-e", "-E", "-s", "-t", "-T", "-v", "-u",
			"--number", "--number-nonblank", "--show-all", "--squeeze-blank"),
	},
	"head": {
		flags:      flags(noArg, "-q", "-v", "--quiet", "--silent", "--verbose").with(valueArg, "-n", "-c", "--lines", "--bytes"),
		numberFlag: true,
	},
	"tail": {
		flags:      flags(noArg, "-q", "-v", "-r", "--quiet", "--silent", "--verbose").with(valueArg, "-n", "-c", "--lines", "--bytes"),
		numberFlag: true,
	},
	"wc": {
		flags: flags(noArg, "-l", "-w", "-c", "-m", "-L",
			"--lines", "--words", "--bytes", "--chars", "--max-line-length"),
	},
	"stat": {
		flags: flags(noArg, "-t", "-x", "--terse").with(valueArg, "-c", "--format", "--printf"),
	},
	"file": {
		flags: flags(noArg, "-b", "-i", "-z", "--brief", "--mime", "--mime-type", "--mime-encoding"),
	},
	"diff": {
		flags: flags(noArg, "-u", "-N", "-r", "-q", "-y", "-w", "-b", "-B", "-i", "-a",
			"-c", "--brief", "--recursive", "--new-file", "--side-by-side",
			"--ignore-all-space", "--ignore-space-change", "--ignore-blank-lines",
			"--ignore-case", "--text").with(valueArg, "-U", "-C", "--unified", "--context", "-W", "--width").with(optionalArg, "--color"),
	},
	"cmp":     {flags: flags(noArg, "-s", "-l", "-b", "--silent", "--quiet", "--verbose", "--print-bytes")},
	"comm":    {flags: flags(noArg, "-1", "-2", "-3", "-i")},
	"nl":      {flags: flags(noArg).with(valueArg, "-b", "-n", "-w", "-s", "-v", "-i")},
	"tac":     {flags: flags(noArg, "-b", "-r")},
	"rev":     {flags: flags(noArg)},
	"strings": {flags: flags(noArg, "-a", "--all").with(valueArg, "-n", "-t", "--bytes")},
	"od": {
		flags: flags(noArg, "-v", "-c", "-x", "-b", "-d", "-o").with(valueArg, "-A", "-t", "-N", "-j"),
	},
	"hexdump": {
		flags: flags(noArg, "-C", "-v", "-b", "-c", "-d", "-o", "-x").with(valueArg, "-n", "-s"),
	},
	"sort": {
		flags: flags(noArg, "-r", "-n", "-u", "-f", "-h", "-V", "-b", "-s", "-M", "-g", "-d", "-z",
			"--reverse", "--numeric-sort", "--unique", "--ignore-case",
			"--human-numeric-sort", "--version-sort", "--stable").with(valueArg, "-k", "-t", "--key", "--field-separator"),
	},
	"uniq": {
		flags: flags(noArg, "-c", "-d", "-u", "-i", "--count", "--repeated", "--unique", "--ignore-case").with(valueArg, "-f", "-s", "-w"),
		// A second operand is the file that uniq writes.
		maxOperands: 1,
	},
	"cut": {
		flags: flags(noArg, "-s", "--complement", "--only-delimited").with(valueArg, "-d", "-f", "-c", "-b", "--delimiter", "--fields", "--characters", "--bytes"),
	},
	"paste":  {flags: flags(noArg, "-s", "--serial").with(valueArg, "-d", "--delimiters")},
	"column": {flags: flags(noArg, "-t", "-x", "-n").with(valueArg, "-s", "-c", "-o")},
	"fold":   {flags: flags(noArg, "-s", "-b").with(valueArg, "-w", "--width")},
	"expand": {flags: flags(noArg, "-i").with(valueArg, "-t", "--tabs")},
	"unexpand": {
		flags: flags(noArg, "-a", "--all", "--first-only").with(valueArg, "-t", "--tabs"),
	},
	"md5sum":    {flags: flags(noArg, "-b", "-t", "-z", "--binary", "--text", "--tag", "--zero")},
	"sha1sum":   {flags: flags(noArg, "-b", "-t", "-z", "--binary", "--text", "--tag", "--zero")},
	"sha256sum": {flags: flags(noArg, "-b", "-t", "-z", "--binary", "--text", "--tag", "--zero")},
	"shasum":    {flags: flags(noArg, "-b", "-t", "-0", "-p").with(valueArg, "-a", "--algorithm")},
	"realpath":  {flags: flags(noArg, "-e", "-m", "-s", "-q", "-z", "--canonicalize-existing", "--canonicalize-missing", "--no-symlinks", "--quiet", "--zero")},
	"readlink":  {flags: flags(noArg, "-f", "-e", "-m", "-n", "-q", "-s", "-v", "-z")},
	"du": {
		flags: flags(noArg, "-s", "-h", "-a", "-c", "-k", "-m", "-x", "--summarize", "--human-readable", "--all", "--total").with(valueArg, "-d", "--max-depth"),
	},
	"tree": {
		flags: flags(noArg, "-a", "-d", "-f", "-x", "-q", "-N", "-Q", "-p", "-u", "-g", "-s", "-h",
			"-D", "-F", "-v", "-t", "-c", "-U", "-r", "-i", "-A", "-S", "-n", "-C",
			"--dirsfirst", "--noreport", "--gitignore", "--prune", "--du", "--si").with(valueArg, "-L", "-I", "-P", "--charset", "--filelimit", "--sort", "--timefmt"),
	},
	"grep":  {flags: grepFlags, operands: patternThenPaths, patternFlags: []string{"-e", "--regexp", "-f", "--file"}},
	"egrep": {flags: grepFlags, operands: patternThenPaths, patternFlags: []string{"-e", "--regexp", "-f", "--file"}},
	"fgrep": {flags: grepFlags, operands: patternThenPaths, patternFlags: []string{"-e", "--regexp", "-f", "--file"}},
	"rg":    {flags: rgFlags, operands: patternThenPaths, patternFlags: []string{"-e", "--regexp", "-f", "--file", "--files", "--type-list"}},
	"echo":  {flags: flags(noArg, "-n", "-e", "-E"), operands: textOperands},
	"printf": {
		flags:    flags(noArg),
		operands: textOperands,
	},
	"tr": {
		flags:       flags(noArg, "-d", "-s", "-c", "-C", "--delete", "--squeeze-repeats", "--complement"),
		operands:    textOperands,
		maxOperands: 2,
	},
	"basename": {flags: flags(noArg, "-a", "-z").with(valueArg, "-s"), operands: textOperands},
	"dirname":  {flags: flags(noArg, "-z"), operands: textOperands},
	"which":    {flags: flags(noArg, "-a", "-s"), operands: textOperands},
	"pwd":      {flags: flags(noArg, "-L", "-P"), operands: noOperands},
	"true":     {flags: flags(noArg), operands: textOperands},
	"false":    {flags: flags(noArg), operands: textOperands},
	"whoami":   {flags: flags(noArg), operands: noOperands},
	"uname": {
		flags:    flags(noArg, "-a", "-s", "-r", "-m", "-n", "-p", "-v", "-o", "--all"),
		operands: noOperands,
	},
}

// gitLogFlags are the flags of git log and git show that only read.
var gitLogFlags = flags(noArg,
	"--oneline", "--shortstat", "--numstat", "--name-only",
	"--name-status", "-p", "--patch", "-s", "--no-patch", "--graph",
	"--no-decorate", "--all", "--branches", "--tags",
	"--remotes", "-i", "--regexp-ignore-case", "--reverse",
	"--first-parent", "--no-merges", "--merges", "--abbrev-commit",
	"--no-abbrev-commit", "--follow", "--no-color", "-w",
	"--full-history", "--date-order", "--topo-order", "--raw",
	"--no-ext-diff", "--no-textconv", "--summary", "--left-right",
	"--cherry-pick", "--boundary", "-m", "--source",
).with(valueArg, "-n", "--max-count", "--skip", "--format", "--pretty", "--author", "--committer", "--since", "--until", "--after", "--before", "--grep", "--date", "-S", "-G", "-L", "--diff-filter", "-U", "--unified").with(optionalArg, "--decorate", "--color", "--abbrev", "--word-diff", "--stat")

// gitDiffFlags are the flags of git diff that only read. --output
// writes a file, and --ext-diff and --no-index run or read past the
// repository.
var gitDiffFlags = flags(noArg,
	"--shortstat", "--numstat", "--name-only", "--name-status",
	"--cached", "--staged", "-p", "--patch", "-s", "--no-patch", "-w",
	"-b", "--ignore-all-space", "--ignore-space-change", "--exit-code",
	"--quiet", "-R", "-M", "--no-renames", "--no-ext-diff", "--no-textconv",
	"--minimal", "--patience", "--histogram", "--no-color", "--raw", "--summary", "--check", "--full-index",
	"--binary", "-a", "--text",
).with(valueArg, "-U", "--unified", "--diff-filter").with(optionalArg, "--color", "--word-diff", "--abbrev", "--stat", "--relative")

// gitCommands are the git subcommands that the parser knows. Each
// operand is a path, a revision, or a pattern. The parser checks each
// one as a path, so a path outside the roots makes the command ask.
var gitCommands = map[string]commandSpec{
	"status": {
		flags: flags(noArg, "-s", "--short", "-b", "--branch", "--long", "-v", "--verbose",
			"-z", "--show-stash", "--ahead-behind", "--no-ahead-behind",
			"--no-renames", "-u").with(optionalArg, "--porcelain", "--untracked-files", "--ignored"),
	},
	"log":  {flags: gitLogFlags, numberFlag: true},
	"show": {flags: gitLogFlags},
	"diff": {flags: gitDiffFlags},
	"branch": {
		flags: flags(noArg, "-a", "-r", "-v", "-vv", "--all", "--remotes", "--verbose",
			"--show-current", "--merged", "--no-merged", "--no-color").with(optionalArg, "--color").with(valueArg, "--sort", "--format", "--contains", "--no-contains", "--points-at"),
		operands: noOperands,
	},
	"rev-parse": {
		flags: flags(noArg, "--show-toplevel", "--abbrev-ref", "--git-dir",
			"--is-inside-work-tree", "--verify", "-q", "--quiet",
			"--show-prefix", "--symbolic-full-name",
			"--git-common-dir", "--show-cdup").with(optionalArg, "--short"),
	},
	"ls-files": {
		flags: flags(noArg, "-m", "-o", "-d", "-c", "-s", "-z", "--others",
			"--modified", "--deleted", "--cached", "--stage",
			"--exclude-standard", "--full-name"),
	},
	"blame": {
		flags: flags(noArg, "-w", "-s", "-e", "-p", "--porcelain", "-l", "-t").with(valueArg, "-L"),
	},
	"remote": {flags: flags(noArg, "-v", "--verbose"), operands: noOperands},
	"tag":    {flags: flags(noArg, "-l", "--list").with(valueArg, "-n", "--sort", "--contains", "--points-at"), operands: noOperands},
	"describe": {
		flags: flags(noArg, "--tags", "--always", "--long", "--all", "--exact-match").with(valueArg, "--match", "--exclude").with(optionalArg, "--abbrev"),
	},
	"merge-base": {flags: flags(noArg, "--is-ancestor", "--all", "--octopus")},
}

// gitGlobalFlags are the flags of git, before the subcommand, that only
// read. -C and -c change the folder or the config, so they are not here.
var gitGlobalFlags = []string{"--no-pager"}

// findFlags are the primaries of find that only read, with the number
// of values of each. -exec, -delete, and the -fprint family are not
// here. -newer reads its value as a path.
var findFlags = flagSet{
	"-name":      valueArg,
	"-iname":     valueArg,
	"-path":      valueArg,
	"-ipath":     valueArg,
	"-wholename": valueArg,
	"-regex":     valueArg,
	"-iregex":    valueArg,
	"-type":      valueArg,
	"-maxdepth":  valueArg,
	"-mindepth":  valueArg,
	"-mtime":     valueArg,
	"-mmin":      valueArg,
	"-atime":     valueArg,
	"-amin":      valueArg,
	"-ctime":     valueArg,
	"-cmin":      valueArg,
	"-size":      valueArg,
	"-perm":      valueArg,
	"-user":      valueArg,
	"-group":     valueArg,
	"-newer":     pathArg,
	"-empty":     noArg,
	"-print":     noArg,
	"-print0":    noArg,
	"-prune":     noArg,
	"-not":       noArg,
	"-a":         noArg,
	"-and":       noArg,
	"-o":         noArg,
	"-or":        noArg,
	"-depth":     noArg,
	"-xdev":      noArg,
	"-mount":     noArg,
	"-ls":        noArg,
	"-true":      noArg,
	"-false":     noArg,
	"!":          noArg,
	"(":          noArg,
	")":          noArg,
}

// cdCommand changes the folder of the parts after it.
const cdCommand = "cd"

// devNull is the one file that a read-only command can write to.
const devNull = "/dev/null"

// numberFlagPattern matches a count flag, such as -5 in head -5.
var numberFlagPattern = regexp.MustCompile(`^-[0-9]+$`)

// readOnlyPaths reports if each part of command is a read-only command,
// and returns the absolute path of each file or folder that the command
// reads. dir is the folder that the command starts in, and home is the
// home folder. A cd in one part changes the folder of the later parts.
// A relative path resolves in each folder that a cd can give. A pipe
// runs each part in its own shell, so a cd before a pipe cannot hide a
// path.
func readOnlyPaths(command, dir, home string) (paths []string, ok bool) {
	parts, substitution := splitCommand(command)
	if substitution || len(parts) == 0 {
		return nil, false
	}
	p := readOnlyParser{
		cwds: []string{dir},
		home: home,
	}
	for _, part := range parts {
		if !p.part(part) {
			return nil, false
		}
	}
	return p.paths, true
}

// readOnlyParser holds the state of [readOnlyPaths].
type readOnlyParser struct {
	// cwds are the folders that a relative path can be relative to.
	cwds  []string
	home  string
	paths []string
}

// part parses one simple command, and adds the paths that it reads.
func (p *readOnlyParser) part(text string) bool {
	words, inputs, ok := lexPart(text)
	if !ok || len(words) == 0 {
		return false
	}
	for _, in := range inputs {
		if !p.addPath(in) {
			return false
		}
	}

	name := words[0]
	if name.quoted || name.glob || name.tilde || strings.ContainsAny(name.text, "/=") {
		return false
	}
	args := words[1:]
	switch name.text {
	case cdCommand:
		return p.cd(args)
	case "git":
		return p.git(args)
	case "find":
		return p.find(args)
	}
	spec, known := readOnlyCommands[name.text]
	if !known {
		return false
	}
	return p.command(spec, args)
}

// cd parses the arguments of cd. A cd with no folder goes to the home
// folder. A cd with a flag, with -, or with two folders asks.
func (p *readOnlyParser) cd(args []word) bool {
	target := word{text: "~", tilde: true}
	switch len(args) {
	case 0:
	case 1:
		target = args[0]
	default:
		return false
	}
	isFlag := strings.HasPrefix(target.text, "-") && !target.quoted
	if isFlag || target.glob {
		return false
	}
	abs := p.resolve(target)
	if !p.addPath(target) {
		return false
	}
	for _, a := range abs {
		if !slices.Contains(p.cwds, a) {
			p.cwds = append(p.cwds, a)
		}
	}
	return true
}

// git parses the arguments of git: the global flags, the subcommand,
// and its arguments.
func (p *readOnlyParser) git(args []word) bool {
	for len(args) > 0 && slices.Contains(gitGlobalFlags, args[0].text) {
		args = args[1:]
	}
	if len(args) == 0 || args[0].quoted {
		return false
	}
	spec, known := gitCommands[args[0].text]
	if !known {
		return false
	}
	return p.command(spec, args[1:])
}

// find parses the arguments of find. The operands before the first
// primary are the folders that find walks.
func (p *readOnlyParser) find(args []word) bool {
	i := 0
	for ; i < len(args); i++ {
		a := args[i]
		startsExpression := !a.quoted && strings.HasPrefix(a.text, "-") ||
			a.text == "(" || a.text == "!"
		if startsExpression {
			break
		}
		if !p.addPath(a) {
			return false
		}
	}
	if i == 0 {
		p.addCwds()
	}
	for ; i < len(args); i++ {
		kind, known := findFlags[args[i].text]
		if !known {
			return false
		}
		if kind == noArg {
			continue
		}
		i++
		if i == len(args) {
			return false
		}
		if kind == pathArg && !p.addPath(args[i]) {
			return false
		}
	}
	return true
}

// command parses the flags and the operands of a command of spec.
func (p *readOnlyParser) command(spec commandSpec, args []word) bool {
	var ops []word
	patternGiven := false
	endOfFlags := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		isFlag := !endOfFlags && strings.HasPrefix(a.text, "-") && a.text != "-"
		if !isFlag {
			ops = append(ops, a)
			continue
		}
		if a.text == "--" {
			endOfFlags = true
			continue
		}
		if spec.numberFlag && numberFlagPattern.MatchString(a.text) {
			continue
		}
		name, value, hasValue := strings.Cut(a.text, "=")
		isLong := strings.HasPrefix(name, "--")
		if !isLong {
			name, hasValue = a.text, false
		}
		if slices.Contains(spec.patternFlags, name) {
			patternGiven = true
		}
		kind, known := spec.flags[name]
		switch {
		case !known:
			next, ok := p.shortFlags(spec, a, args[i+1:])
			if !ok {
				return false
			}
			i += next
		case hasValue && kind == noArg:
			return false
		case hasValue && kind == pathArg:
			if !p.addPath(word{text: value}) {
				return false
			}
		case hasValue, kind == noArg, kind == optionalArg:
		default:
			i++
			if i == len(args) {
				return false
			}
			if kind == pathArg && !p.addPath(args[i]) {
				return false
			}
		}
	}
	return p.operands(spec, ops, patternGiven)
}

// shortFlags parses a group of short flags, such as -la or -n5. It
// returns how many of the next arguments the group used as a value.
func (p *readOnlyParser) shortFlags(spec commandSpec, a word, rest []word) (int, bool) {
	isLong := strings.HasPrefix(a.text, "--")
	if isLong || a.quoted || len(a.text) < 2 {
		return 0, false
	}
	letters := a.text[1:]
	for j := 0; j < len(letters); j++ {
		name := "-" + letters[j:j+1]
		kind, known := spec.flags[name]
		if !known {
			return 0, false
		}
		if slices.Contains(spec.patternFlags, name) {
			return 0, false
		}
		if kind == noArg {
			continue
		}
		value := letters[j+1:]
		if value != "" {
			return 0, kind != pathArg
		}
		if len(rest) == 0 {
			return 0, false
		}
		if kind == pathArg && !p.addPath(rest[0]) {
			return 0, false
		}
		return 1, true
	}
	return 0, true
}

// operands adds the paths among the operands of a command of spec.
func (p *readOnlyParser) operands(spec commandSpec, ops []word, patternGiven bool) bool {
	if spec.maxOperands > 0 && len(ops) > spec.maxOperands {
		return false
	}
	switch spec.operands {
	case noOperands:
		return len(ops) == 0
	case textOperands:
		for _, op := range ops {
			if op.glob && !p.addPath(op) {
				return false
			}
		}
		return true
	case patternThenPaths:
		if !patternGiven {
			if len(ops) == 0 {
				return false
			}
			ops = ops[1:]
		}
	}
	if len(ops) == 0 {
		p.addCwds()
	}
	for _, op := range ops {
		if !p.addPath(op) {
			return false
		}
	}
	return true
}

// addCwds adds each folder that the command can start in, because a
// command with no path reads its folder.
func (p *readOnlyParser) addCwds() {
	p.paths = append(p.paths, p.cwds...)
}

// resolve returns the absolute path of w in each folder of p.cwds.
func (p *readOnlyParser) resolve(w word) []string {
	text := w.text
	if w.tilde {
		rest := strings.TrimPrefix(text, homeName)
		return []string{filepath.Join(p.home, rest)}
	}
	if filepath.IsAbs(text) {
		return []string{filepath.Clean(text)}
	}
	out := make([]string, 0, len(p.cwds))
	for _, cwd := range p.cwds {
		out = append(out, filepath.Join(cwd, text))
	}
	return out
}

// addPath adds the paths of the word w. For a glob, it adds the folder
// of the glob and each path that the glob matches now. It reports false
// for a glob that can match "..".
func (p *readOnlyParser) addPath(w word) bool {
	if w.text == "-" && !w.quoted {
		return true
	}
	for _, abs := range p.resolve(w) {
		if !w.glob {
			p.paths = append(p.paths, abs)
			continue
		}
		if !safeGlob(w.text) {
			return false
		}
		matches, err := filepath.Glob(abs)
		if err != nil {
			return false
		}
		p.paths = append(p.paths, globDir(abs))
		p.paths = append(p.paths, matches...)
	}
	return true
}

// safeGlob reports if no part of the glob can match "." or "..". A
// shell matches a leading dot only if the pattern starts with one.
func safeGlob(pattern string) bool {
	for part := range strings.SplitSeq(pattern, "/") {
		dotGlob := strings.HasPrefix(part, ".") && strings.ContainsAny(part, globChars)
		if dotGlob {
			return false
		}
	}
	return true
}

// globDir returns the longest folder of the glob abs with no glob
// character.
func globDir(abs string) string {
	i := strings.IndexAny(abs, globChars)
	return filepath.Dir(abs[:i+1])
}

// globChars are the characters that make a word a glob.
const globChars = "*?["
