// Package host holds the parts of a local sandbox that the tools reach
// through an interface.
package host

// Pather is a sandbox with paths on this host. The local sandbox is one.
// The search tools use it to check a path before they give it to rg, and
// to walk the directory when rg is not installed. A remote sandbox is
// isolated already, so it needs no check.
type Pather interface {
	// HostPath returns path as an absolute path on this host. It returns
	// an error for a path that the sandbox does not let the tools read.
	HostPath(path string) (string, error)
}

// Sizer is a sandbox that gives the size of a file without a read of
// the file. The local sandbox is one. The read tool uses it to refuse a
// large file before the file fills the memory, and the walk of the grep
// tool uses it to skip a large file. The interface of a
// remote sandbox has no stat, so a remote file has no size check.
type Sizer interface {
	// FileSize returns the size of a file in bytes. It obeys the same
	// rules as ReadFile, and returns the same errors.
	FileSize(path string) (int64, error)
}
