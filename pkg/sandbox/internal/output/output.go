// Package output holds the limits and the text rules that the tools
// share for the text that they return to the model.
package output

import (
	"bytes"
	"strings"
)

const (
	// MaxLines is the largest number of lines of text that one call of
	// the read or bash tool returns.
	MaxLines = 2000
	// MaxBytes is the largest number of bytes of text that one call of
	// the read or bash tool returns.
	MaxBytes = 50 * 1024
	// SniffLength is the number of bytes at the start of a file that a
	// tool reads to find its type. git uses the same number.
	SniffLength = 8000
)

// IsBinary reports whether data is binary: it has a NUL byte in its
// first SniffLength bytes. A text file has none. git and rg use the
// same test.
func IsBinary(data []byte) bool {
	head := data[:min(len(data), SniffLength)]
	return bytes.IndexByte(head, 0) >= 0
}

// Quote makes s one word for a POSIX shell.
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
