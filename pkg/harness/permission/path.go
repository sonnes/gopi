package permission

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// pathRoot is the directory that a path pattern is relative to.
type pathRoot int

const (
	// rootDir is the working directory of the policy.
	rootDir pathRoot = iota
	// rootFS is the root of the file system.
	rootFS
	// rootHome is the home directory of the user.
	rootHome
)

const (
	// fsRootMark starts a pattern relative to the root of the file
	// system, as in "//etc/**".
	fsRootMark = "//"
	// homeMark starts a pattern relative to the home directory, as in
	// "~/.ssh/**".
	homeMark = "~/"
	// homeName is the home directory in the input of a tool.
	homeName = "~"
	// dirMark starts a pattern relative to the working directory, as in
	// "./.env".
	dirMark = "./"
	// anyDepth makes a glob match at each depth.
	anyDepth = "**/"
	// inside makes a glob match each path in a directory.
	inside = "/**"
	// everything is a glob that matches each path.
	everything = "**"
)

// pathRule is the content of a rule for a file tool: a glob with the
// rules of a .gitignore file, relative to a root. The roots come from
// patternWithRoot in utils/permissions/filesystem.ts of Claude Code:
//
//   - "//path" is relative to the root of the file system.
//   - "~/path" is relative to the home directory.
//   - "/path" is relative to the working directory, and anchored to it.
//   - "./path" and "path" are relative to the working directory.
//
// As in a .gitignore file, a pattern with no slash before its end
// matches at each depth. A pattern that matches a directory matches
// each path in it.
type pathRule struct {
	root pathRoot
	// glob is the doublestar pattern, relative to the root.
	glob string
	// all is true if the pattern matches the root and each path in it.
	all bool
}

// parsePathRule parses the content of a rule for a file tool.
func parsePathRule(content string) (pathRule, error) {
	root := rootDir
	pattern := content
	switch {
	case strings.HasPrefix(content, fsRootMark):
		root = rootFS
		pattern = content[len(fsRootMark)-1:]
	case strings.HasPrefix(content, homeMark):
		root = rootHome
		pattern = content[len(homeMark)-1:]
	case strings.HasPrefix(content, dirMark):
		pattern = content[len(dirMark):]
	}

	r := gitignoreGlob(pattern)
	r.root = root
	valid := doublestar.ValidatePattern(r.glob)
	if !valid {
		return pathRule{}, fmt.Errorf("the path pattern %q is not a valid glob", content)
	}
	return r, nil
}

// gitignoreGlob turns a pattern with the rules of a .gitignore file
// into a doublestar glob.
func gitignoreGlob(pattern string) pathRule {
	p := strings.TrimSuffix(pattern, inside)
	p = strings.TrimSuffix(p, "/")
	anchored := strings.Contains(p, "/")
	p = strings.TrimPrefix(p, "/")

	if p == "" || p == everything {
		return pathRule{all: true}
	}
	if !anchored {
		p = anyDepth + p
	}
	return pathRule{glob: p}
}

// matches reports if the rule matches the absolute path abs. dir is
// the working directory, and home is the home directory. If home is
// empty, a rule relative to the home directory matches no path.
func (r pathRule) matches(abs, dir, home string) bool {
	var root string
	switch r.root {
	case rootFS:
		root = string(filepath.Separator)
	case rootHome:
		root = home
	default:
		root = dir
	}
	if root == "" {
		return false
	}

	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	outside := rel == ".." || strings.HasPrefix(rel, "../")
	if outside {
		return false
	}
	if r.all {
		return true
	}
	if rel == "." {
		return false
	}

	exact, _ := doublestar.Match(r.glob, rel)
	inDir, _ := doublestar.Match(r.glob+inside, rel)
	return exact || inDir
}

// resolvePath returns the absolute path of the "path" input of a tool.
// An empty path is dir, as for grep and find. A path that starts with
// "~/" is relative to home. Another relative path is relative to dir.
func resolvePath(p, dir, home string) string {
	switch {
	case p == "":
		return dir
	case p == homeName:
		return home
	case strings.HasPrefix(p, homeMark):
		return filepath.Join(home, p[len(homeMark):])
	case filepath.IsAbs(p):
		return filepath.Clean(p)
	}
	return filepath.Join(dir, p)
}
