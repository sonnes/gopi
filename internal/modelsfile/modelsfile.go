// Package modelsfile reads and writes the format of models.json: the
// default models of each provider and each driver, by ID. The command
// cmd/modelsync writes the file, and
// [github.com/sonnes/gopi/pkg/catalog.ReadModelFile] reads it.
package modelsfile

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/sonnes/gopi/pkg/ai"
)

// The kinds of an entry.
const (
	KindProvider = "provider"
	KindDriver   = "driver"
)

// indent is the indent of one level of the file.
const indent = "  "

// File is the content of models.json, by provider or driver ID.
type File map[string]Entry

// Entry is the models of one provider or one driver.
type Entry struct {
	// Kind is [KindProvider] or [KindDriver].
	Kind string `json:"kind"`
	// Default is the ID of the model that a caller uses when it names
	// no model.
	Default string `json:"default"`
	// Models are sorted by ID.
	Models []Model `json:"models"`
}

// Model is one model of the file. Note is for a person to read, such as
// the reason for a value.
type Model struct {
	ai.Model
	Note string `json:"note,omitempty"`
}

// Parse decodes a file. It rejects an unknown field, so that a typo in
// the file fails and does not drop a value.
func Parse(b []byte) (File, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var f File
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("modelsfile: decode models.json: %w", err)
	}
	return f, nil
}

// Marshal encodes f in the form of models.json: the IDs and the models
// sorted, two spaces for each level, and a newline at the end. A file
// that the generator writes then differs from the old one only where a
// value changed.
func Marshal(f File) ([]byte, error) {
	sorted := make(File, len(f))
	for id, e := range f {
		e.Models = slices.Clone(e.Models)
		slices.SortFunc(e.Models, func(a, b Model) int {
			return cmp.Compare(a.ID, b.ID)
		})
		sorted[id] = e
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", indent)
	if err := enc.Encode(sorted); err != nil {
		return nil, fmt.Errorf("modelsfile: encode models.json: %w", err)
	}
	return buf.Bytes(), nil
}
