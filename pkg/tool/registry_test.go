package tool

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestRegistryNamesAreUnique checks that each gopi name and each Claude
// Code name of the registry names one tool, and maps back to it.
func TestRegistryNamesAreUnique(t *testing.T) {
	seen := map[string]string{}
	for _, k := range knownNames {
		for _, n := range append([]string{k.Name}, k.Claude...) {
			other, dup := seen[n]
			assert.False(t, dup, "%q names both %s and %s", n, other, k.Name)
			seen[n] = k.Name
			assert.Equal(t, k.Name, Canonical(n), n)
		}
	}
}
