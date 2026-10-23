package def

import (
	"encoding/json"
	"fmt"
)

// Extra holds the frontmatter keys that no field takes, with their
// values as the reader parsed them, such as "metadata", "license", and
// "hooks". Each value is a scalar, a list, or a map, so a store can
// keep it as JSON.
type Extra map[string]any

// Read returns the value of key as a T. It converts the value as
// [encoding/json] does: a map goes into a struct or a map, and a list
// into a slice. If the key is missing, ok is false and v is the zero
// value. Read has three results, as [session.Value.Read] has.
//
// A caller reads the "metadata" key of the Agent Skills format as:
//
//	type Meta struct {
//		Owner string `json:"owner"`
//	}
//	meta, ok, err := a.Extra.Read[Meta]("metadata")
func (e Extra) Read[T any](key string) (v T, ok bool, err error) {
	raw, ok := e[key]
	if !ok {
		return v, false, nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return v, true, fmt.Errorf("def: extra key %q: %w", key, err)
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return v, true, fmt.Errorf("def: extra key %q: %w", key, err)
	}
	return v, true, nil
}
