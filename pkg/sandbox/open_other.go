//go:build !darwin

package sandbox

import "os"

// getPath returns the name of f. Only macOS has a fence backend, so
// only macOS asks the kernel for the path of an open file.
func getPath(f *os.File) (string, error) {
	return f.Name(), nil
}
