package harness

import (
	"errors"
	"fmt"

	"github.com/sonnes/gopi/pkg/sandbox"
	"github.com/sonnes/gopi/pkg/sandbox/bash"
	"github.com/sonnes/gopi/pkg/sandbox/edit"
	"github.com/sonnes/gopi/pkg/sandbox/fence"
	"github.com/sonnes/gopi/pkg/sandbox/find"
	"github.com/sonnes/gopi/pkg/sandbox/grep"
	"github.com/sonnes/gopi/pkg/sandbox/read"
	"github.com/sonnes/gopi/pkg/sandbox/write"
	"github.com/sonnes/gopi/pkg/tool"
	"github.com/sonnes/gopi/pkg/tool/todowrite"
)

// Builtins is a set of built-in tools. See [Builtin].
type Builtins int

const (
	// Coding gives the tools that read files, change files, and run
	// commands in the sandbox, and todo_write.
	Coding Builtins = iota
	// ReadOnly gives the tools that read files in the sandbox, and
	// todo_write.
	ReadOnly
	// NoBuiltins gives no built-in tool.
	NoBuiltins
)

// builtinTools returns the built-in tools of h for dir, in the order
// that the model gets them.
func (h *Harness) builtinTools(dir string) ([]tool.Tool, error) {
	if h.builtin == NoBuiltins {
		return nil, nil
	}
	box, err := h.sandbox(dir)
	if err != nil {
		return nil, err
	}

	reads := []tool.Tool{
		read.New(box),
		grep.New(box),
		find.New(box),
	}
	if h.builtin == ReadOnly {
		return append(reads, todowrite.New()), nil
	}
	return []tool.Tool{
		read.New(box),
		write.New(box),
		edit.New(box),
		bash.New(box),
		grep.New(box),
		find.New(box),
		todowrite.New(),
	}, nil
}

// sandbox returns the [Sandbox] of h, or a local sandbox in dir with the
// workspace fence.
func (h *Harness) sandbox(dir string) (sandbox.Sandbox, error) {
	if h.box != nil {
		return h.box, nil
	}
	box, err := sandbox.Local(dir, sandbox.Policy(fence.Workspace(dir)...))
	if errors.Is(err, errors.ErrUnsupported) {
		return nil, fmt.Errorf("harness: %w: the fence of the default sandbox runs only on macOS. Give a sandbox with harness.Sandbox, or use harness.Builtin(harness.NoBuiltins)", err)
	}
	if err != nil {
		return nil, fmt.Errorf("harness: the sandbox: %w", err)
	}
	return box, nil
}
