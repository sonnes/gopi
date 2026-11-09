// Package modelsfile reads and writes the format of models.json: the
// default models of each provider and each driver, by ID. The command
// cmd/modelsync writes the file, and
// [github.com/sonnes/gopi/pkg/catalog.ReadModelFile] reads it. [Get]
// downloads a file of model data for both of them.
package modelsfile

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"

	"github.com/sonnes/gopi/pkg/ai"
)

// ErrTooLarge is the error of a body that is larger than the limit of
// [Get].
var ErrTooLarge = errors.New("modelsfile: the body is larger than the limit")

// indent is the indent of one level of the file.
const indent = "  "

// File is the content of models.json, by provider or driver ID.
type File map[string]Entry

// Entry is the models of one provider or one driver.
type Entry struct {
	// Default is the ID of the model that a caller uses when it names
	// no model. It is the ID of one of Models.
	Default string `json:"default"`
	// Models are sorted by ID.
	Models []Model `json:"models"`
}

// Model is one model of the file. A zero or missing field is unknown.
// Note is for a person to read, such as the reason for a value, because
// JSON has no comments. gopi does not read it.
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

// Get returns the body of a GET of url with [http.DefaultClient]. A
// status other than 200 is an error. A body of more than limit bytes
// fails with [ErrTooLarge], because a cut body is not the file.
func Get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("%w of %d bytes", ErrTooLarge, limit)
	}
	return body, nil
}
