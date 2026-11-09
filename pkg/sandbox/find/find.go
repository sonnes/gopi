// Package find is the find tool. The tool lists the files that match a
// glob, with rg.
package find

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/sonnes/gopi/pkg/sandbox"
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

// Name is the name of the tool.
const Name = tool.Find

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
	root, err := search.NewRoot(box, in.Path)
	if err != nil {
		return "", err
	}

	args := []string{
		"--no-config",
		"--files",
		"--sortr=modified",
		"--glob", search.SkipHidden,
	}
	res, err := root.RunRG(ctx, args)
	if err != nil {
		return "", err
	}
	if res.ExitCode == search.ExitNotFound {
		return walkFind(root, in)
	}

	res.Stdout = []byte(root.FilterGlob(string(res.Stdout), in.Pattern, false))
	out, ok := search.Output(res, search.Limit(in.Limit, findDefaultLimit))
	if !ok {
		return "", search.Error(res)
	}
	if out == "" {
		return "No files found", nil
	}
	return out, nil
}

// walkFind is the find tool without rg. It walks the directory on this
// host. rg lists a root that is a file if its name matches, and so does
// the walk.
func walkFind(root search.Root, in findInput) (string, error) {
	if !doublestar.ValidatePattern(in.Pattern) {
		return "", fmt.Errorf("invalid glob: %s", in.Pattern)
	}
	files, err := root.Walk()
	if err != nil {
		return "", err
	}

	var found []search.File
	for _, f := range files {
		if search.MatchGlob(in.Pattern, f.Rel) {
			found = append(found, f)
		}
	}
	if len(found) == 0 {
		return "No files found", nil
	}

	slices.SortStableFunc(found, func(a, b search.File) int {
		return b.ModTime.Compare(a.ModTime)
	})
	paths := make([]string, len(found))
	for i, f := range found {
		paths[i] = f.Path
	}
	return search.CapLines(strings.Join(paths, "\n"), search.Limit(in.Limit, findDefaultLimit)), nil
}
