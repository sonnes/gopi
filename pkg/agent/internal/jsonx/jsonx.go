// Package jsonx compares JSON values that an agent CLI and a caller
// exchange.
package jsonx

import (
	"encoding/json"
	"reflect"
)

// Same reports whether a and b hold the same JSON value. A caller that
// encodes a value again can change the spaces or the order of the keys,
// and the value stays the same. If a or b is not valid JSON, Same
// returns false.
func Same(a, b json.RawMessage) bool {
	var va, vb any
	if json.Unmarshal(a, &va) != nil || json.Unmarshal(b, &vb) != nil {
		return false
	}
	return reflect.DeepEqual(va, vb)
}
