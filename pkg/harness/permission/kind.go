package permission

import "github.com/sonnes/gopi/pkg/tool"

// kindOf returns the kind of the tool with the name. An entry in
// overrides wins over the kind in the registry of [tool.Lookup]. A
// tool that is in neither is [tool.KindOther].
func kindOf(name string, overrides map[string]tool.Kind) tool.Kind {
	k, ok := overrides[name]
	if ok {
		return k
	}
	return known(name).Kind
}

// contentOf returns what the content of a rule on the tool with the
// name matches, from the registry of [tool.Lookup].
func contentOf(name string) tool.Content {
	return known(name).Content
}

// known returns the well-known tool whose gopi name is name. It
// returns the zero [tool.Known] for each other name. A call names a
// tool by its gopi name, so a call of "Grep" is a call of a tool of
// the caller, and gets no kind of a built-in tool.
func known(name string) tool.Known {
	k, ok := tool.Lookup(name)
	if !ok || k.Name != name {
		return tool.Known{}
	}
	return k
}
