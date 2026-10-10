// Package write is the write_file tool. The tool creates a file, or
// replaces the content of a file that exists.
package write

import (
	"context"
	_ "embed"
	"errors"
	"fmt"

	"github.com/sonnes/gopi/pkg/sandbox"
	"github.com/sonnes/gopi/pkg/sandbox/internal/pathlock"
	"github.com/sonnes/gopi/pkg/tool"
)

// writeInput is the input of the write_file tool.
type writeInput struct {
	Path    string `json:"path" jsonschema:"file path"`
	Content string `json:"content" jsonschema:"file content"`
}

// Name is the name of the tool.
const Name = "write_file"

// description is the text that the model reads for the tool.
//
//go:embed description.md
var description string

// New returns the write_file tool on box. The tool creates a file and
// its parent directories, or replaces the content of a file that
// exists.
func New(box sandbox.Sandbox) tool.Tool {
	return tool.New(Name, func(ctx context.Context, in writeInput) (string, error) {
		if in.Path == "" {
			return "", errors.New("path is required")
		}
		// An edit of this path holds the lock while it changes the file.
		unlock := pathlock.Lock(box.Dir(), in.Path)
		defer unlock()
		if err := box.WriteFile(ctx, in.Path, []byte(in.Content)); err != nil {
			return "", err
		}
		return fmt.Sprintf("Wrote %d bytes to %s", len(in.Content), in.Path), nil
	}, tool.Description(description))
}
