// Package find is the find tool. The tool lists the files that match a
// glob, with rg.
package find

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/sonnes/gopi/pkg/sandbox"
	"github.com/sonnes/gopi/pkg/sandbox/internal/host"
	"github.com/sonnes/gopi/pkg/sandbox/internal/search"
	"github.com/sonnes/gopi/pkg/tool"
)

// findDefaultLimit is the number of paths that the find tool returns
// when the model gives no limit.
const findDefaultLimit = 1000

// findInput is the input of the find tool.
type findInput struct {
	Pattern string `json:"pattern" jsonschema:"glob, e.g. **/*.go"`
	Path    string `json:"path,omitempty" jsonschema:"directory to search, default the working directory"`
	Limit   *int   `json:"limit,omitempty" jsonschema:"max paths, default 1000"`
}

func (in findInput) limit() int {
	if in.Limit != nil && *in.Limit > 0 {
		return *in.Limit
	}
	return findDefaultLimit
}

// Name is the name of the tool.
const Name = "find"

// description is the text that the model reads for the tool.
//
//go:embed description.md
var description string

// New returns the find tool on box. The tool lists the files that match
// a glob, with rg, one path on each line. The file that changed last
// comes first, because a model that looks for a file usually wants one
// that it or the user changed recently.
func New(box sandbox.Sandbox) tool.Tool {
	return tool.New(Name, func(ctx context.Context, in findInput) (string, error) {
		return find(ctx, box, in)
	}, tool.Description(description))
}

func find(ctx context.Context, box sandbox.Sandbox, in findInput) (string, error) {
	if in.Pattern == "" {
		return "", errors.New("pattern is required")
	}
	base, err := search.Base(box, in.Path)
	if err != nil {
		return "", err
	}

	args := []string{
		"--no-config",
		"--files",
		"--sortr=modified",
		"--glob", search.SkipHidden,
	}
	res, err := search.RunRG(ctx, box, base, args)
	if err != nil {
		return "", err
	}
	if res.ExitCode == search.ExitNotFound {
		hp, ok := box.(host.Pather)
		if !ok {
			return "", search.ErrNoRG
		}
		return walkFind(hp, base, in)
	}

	res.Stdout = []byte(search.FilterGlob(string(res.Stdout), base, in.Pattern, false))
	out, ok := search.Output(res, in.limit())
	if !ok {
		return "", search.Error(res)
	}
	if out == "" {
		return "No files found", nil
	}
	return out, nil
}

// walkFind is the find tool without rg. It walks the directory on this
// host.
func walkFind(hp host.Pather, base string, in findInput) (string, error) {
	if !doublestar.ValidatePattern(in.Pattern) {
		return "", fmt.Errorf("invalid glob: %s", in.Pattern)
	}

	root, err := hp.HostPath(base)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	// rg lists a file path that matches. The walk skips its root.
	if !info.IsDir() {
		if search.MatchGlob(in.Pattern, filepath.Base(root)) {
			return base, nil
		}
		return "No files found", nil
	}
	rels, err := search.WalkFiles(hp, root)
	if err != nil {
		return "", err
	}

	type file struct {
		path    string
		modTime time.Time
	}
	var files []file
	for _, rel := range rels {
		if !search.MatchGlob(in.Pattern, rel) {
			continue
		}
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		files = append(files, file{path: path.Join(base, rel), modTime: info.ModTime()})
	}
	if len(files) == 0 {
		return "No files found", nil
	}

	slices.SortStableFunc(files, func(a, b file) int {
		return b.modTime.Compare(a.modTime)
	})
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.path
	}
	return search.CapLines(strings.Join(paths, "\n"), in.limit()), nil
}
