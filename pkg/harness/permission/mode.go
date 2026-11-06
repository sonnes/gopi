package permission

import (
	"fmt"
	"slices"
)

// Mode decides the calls that no rule matches. The names are the names
// of the permission modes of Claude Code.
type Mode string

const (
	// Default runs the read tools and the read-only bash commands in the
	// roots. Each other call asks.
	Default Mode = "default"
	// AcceptEdits runs what Default runs, and the edit tools in the
	// roots, except on a protected path. Each other call asks.
	AcceptEdits Mode = "acceptEdits"
	// Bypass runs each call. A Deny rule, an Ask rule, and a hard limit
	// still apply.
	Bypass Mode = "bypassPermissions"
)

// ParseMode returns the mode with the name s. An empty s gives
// [Default]. Another name gives an error.
func ParseMode(s string) (Mode, error) {
	if s == "" {
		return Default, nil
	}
	m := Mode(s)
	if !slices.Contains(modeOrder, m) {
		return "", fmt.Errorf("permission: unknown mode %q", s)
	}
	return m, nil
}

// modeOrder lists the modes from the strictest to the least strict.
var modeOrder = []Mode{
	Default,
	AcceptEdits,
	Bypass,
}

// Stricter returns the stricter mode of a and b. The order is
// [Default], [AcceptEdits], [Bypass], from the strictest. An empty mode
// is [Default]. An unknown mode is as strict as [Default], so an error
// in a name does not give more access.
func Stricter(a, b Mode) Mode {
	a = normalMode(a)
	b = normalMode(b)
	aFirst := slices.Index(modeOrder, a) <= slices.Index(modeOrder, b)
	if aFirst {
		return a
	}
	return b
}

// normalMode returns [Default] for an empty mode and for an unknown
// mode.
func normalMode(m Mode) Mode {
	if !slices.Contains(modeOrder, m) {
		return Default
	}
	return m
}
