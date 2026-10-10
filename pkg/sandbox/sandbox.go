package sandbox

import (
	"context"
	"os"
	"path/filepath"

	"github.com/sonnes/gopi/pkg/sandbox/internal/host"
)

// Sandbox is the place where the tools read files, write files, and run
// commands. A path is absolute, or relative to [Sandbox.Dir].
//
// The interface is small, so that a remote sandbox can implement it
// with one call for each method. The tools build every other operation,
// such as a search or an edit, from these methods.
type Sandbox interface {
	// Exec runs a shell command and waits for it to end. A command that
	// ends with a nonzero exit code is not an error: Result.ExitCode
	// holds the code. Exec returns an error when the command cannot
	// start, or when ctx ends. When ctx ends, Exec stops the command and
	// returns the output that it has.
	Exec(ctx context.Context, cmd Command) (Result, error)

	// ReadFile returns the content of a file. For a file that does not
	// exist, the error wraps [io/fs.ErrNotExist].
	ReadFile(ctx context.Context, path string) ([]byte, error)

	// WriteFile creates or replaces a file. It creates the parent
	// directories of the file. The write is not atomic: during the
	// write, a reader can see a partial file, and after a crash, the
	// file can stay partial. [Local] truncates the file and then writes
	// it in place.
	WriteFile(ctx context.Context, path string, data []byte) error

	// Dir is the absolute working directory of the sandbox.
	Dir() string
}

// Command is one shell command for [Sandbox.Exec].
type Command struct {
	// Line is the command line. A POSIX shell runs it, so it can hold
	// pipes, redirects, and more than one command.
	Line string
	// Dir is the directory that the command starts in. It is absolute,
	// or relative to [Sandbox.Dir]. If Dir is empty, the command starts
	// in [Sandbox.Dir]. If Dir is not a directory, Exec returns an error.
	Dir string
}

// Result is the output of one [Command].
type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// remoteScratchDir is the scratch directory of a sandbox without host
// paths. Every POSIX system has /tmp.
const remoteScratchDir = "/tmp/gopi"

// ScratchDir returns the directory in box where a tool keeps a file for
// the model, such as the full output of a long command. For a sandbox
// on this host, it is in the temp directory of this process, so the
// policy [github.com/sonnes/gopi/pkg/sandbox/fence.Workspace] can write
// it. For a remote sandbox, the temp directory of this host is not a
// path on the remote machine, so ScratchDir returns /tmp/gopi.
func ScratchDir(box Sandbox) string {
	if _, ok := box.(host.Pather); ok {
		return filepath.Join(os.TempDir(), "gopi")
	}
	return remoteScratchDir
}
