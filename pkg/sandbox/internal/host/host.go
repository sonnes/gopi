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
