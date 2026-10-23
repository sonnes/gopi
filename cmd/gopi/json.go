package main

import (
	"encoding/json"
	"io"

	"github.com/sonnes/gopi/pkg/agent"
)

// printer shows the events of a run.
type printer interface {
	print(e agent.Event)
}

// jsonLines prints each event as one line of JSON, for a program that
// reads the output of gopi. [agent.UnmarshalEvent] reads a line back.
type jsonLines struct {
	w io.Writer
}

func (p jsonLines) print(e agent.Event) {
	// An event always marshals, and a failed write to stdout has no
	// other place to go.
	_ = json.NewEncoder(p.w).Encode(e)
}
