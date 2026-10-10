// Package edit is the edit_file tool. The tool replaces exact text in a
// file.
package edit

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strings"

	"github.com/sonnes/gopi/pkg/sandbox"
	"github.com/sonnes/gopi/pkg/sandbox/internal/pathlock"
	"github.com/sonnes/gopi/pkg/tool"
)

// editInput is the input of the edit_file tool.
type editInput struct {
	Path       string `json:"path" jsonschema:"file path"`
	OldString  string `json:"old_string" jsonschema:"exact text to replace"`
	NewString  string `json:"new_string" jsonschema:"replacement text"`
	ReplaceAll bool   `json:"replace_all,omitempty" jsonschema:"replace every occurrence"`
}

// Name is the name of the tool.
const Name = "edit_file"

// description is the text that the model reads for the tool.
//
//go:embed description.md
var description string

// New returns the edit_file tool on box. The tool replaces old_string
// with new_string. old_string must occur one time, unless replace_all
// is set. The tool takes one replacement for each call, because a list
// of replacements needs rules for an overlap and for the order of the
// changes.
func New(box sandbox.Sandbox) tool.Tool {
	return tool.New(Name, func(ctx context.Context, in editInput) (string, error) {
		return edit(ctx, box, in)
	}, tool.Description(description))
}

func edit(ctx context.Context, box sandbox.Sandbox, in editInput) (string, error) {
	if in.OldString == "" {
		return "", errors.New("old_string is required")
	}
	if in.OldString == in.NewString {
		return "", errors.New("old_string and new_string are the same")
	}

	// The agent can run two calls at the same time. The lock keeps an
	// edit of this path from losing the change of another.
	unlock := pathlock.Lock(box.Dir(), in.Path)
	defer unlock()

	data, err := box.ReadFile(ctx, in.Path)
	if err != nil {
		return "", err
	}
	content := string(data)

	// The model writes each line ending as LF. A file with CRLF gets
	// its own line ending in both strings. A file with both line endings
	// can hold old_string as it is, so the tool tries it first.
	oldString, newString := in.OldString, in.NewString
	if !strings.Contains(content, oldString) && strings.Contains(content, "\r\n") {
		oldString = toCRLF(oldString)
		newString = toCRLF(newString)
	}

	count := strings.Count(content, oldString)
	if count == 0 {
		return "", errors.New("old_string not found in the file")
	}
	if count > 1 && !in.ReplaceAll {
		return "", fmt.Errorf("old_string occurs %d times: add surrounding text to make it unique, or set replace_all", count)
	}

	content = strings.ReplaceAll(content, oldString, newString)
	if err := box.WriteFile(ctx, in.Path, []byte(content)); err != nil {
		return "", err
	}
	return "Edited " + in.Path, nil
}

// toCRLF changes each LF in s that has no CR before it to CRLF.
func toCRLF(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\n", "\r\n")
}
