package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/sonnes/gopi/internal/modelsfile"
	"github.com/sonnes/gopi/pkg/ai"
)

// maxModelFile caps the size of a model file that [ReadModelFile]
// reads. The file is near 100 KB, so a larger body is not a model file.
const maxModelFile = 8 << 20

// ModelFile is one models.json: the models of each provider and each
// driver, keyed by the ID that its detector registers, such as
// "anthropic" or "claude-code". make models writes the file, and a
// program reads it with [ReadModelFile]. gopi does not embed it, so a
// program chooses where the file comes from and when to read it again.
// The zero ModelFile has no models.
type ModelFile struct {
	file modelsfile.File
}

// ParseModelFile returns the ModelFile of data. It accepts a field that
// it does not know, because a file of a newer gopi must still load in an
// older program.
func ParseModelFile(data []byte) (ModelFile, error) {
	var f modelsfile.File
	if err := json.Unmarshal(data, &f); err != nil {
		return ModelFile{}, fmt.Errorf("catalog: decode the model file: %w", err)
	}
	return ModelFile{file: f}, nil
}

// ReadModelFile reads the model file at src: a path, or an http or
// https URL. It makes no copy and keeps no cache. A program that wants
// one writes the file where it chooses, and reads that path.
func ReadModelFile(ctx context.Context, src string) (ModelFile, error) {
	data, err := readSource(ctx, src)
	if err != nil {
		return ModelFile{}, fmt.Errorf("catalog: read the model file %s: %w", src, err)
	}
	return ParseModelFile(data)
}

// readSource returns the content of the path or the URL src.
func readSource(ctx context.Context, src string) ([]byte, error) {
	isURL := strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://")
	if !isURL {
		return os.ReadFile(src)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
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
	return io.ReadAll(io.LimitReader(resp.Body, maxModelFile))
}

// MarshalJSON returns the file in the form of models.json, so a program
// can keep a copy of the file that it read.
func (f ModelFile) MarshalJSON() ([]byte, error) {
	return modelsfile.Marshal(f.file)
}

// Of returns the models of the provider or driver id, with the default
// model first and the others in ID order. A detector registers them in
// this order. It returns nil for an ID that the file does not have.
func (f ModelFile) Of(id string) []ai.Model {
	e, ok := f.file[id]
	if !ok {
		return nil
	}
	out := make([]ai.Model, 0, len(e.Models))
	for _, m := range e.Models {
		if m.ID == e.Default {
			out = append(out, cloneModel(m.Model))
		}
	}
	for _, m := range e.Models {
		if m.ID != e.Default {
			out = append(out, cloneModel(m.Model))
		}
	}
	return out
}

// Lookup returns the model modelID of the provider or driver id.
func (f ModelFile) Lookup(id, modelID string) (ai.Model, bool) {
	e, ok := f.file[id]
	if !ok {
		return ai.Model{}, false
	}
	for _, m := range e.Models {
		if m.ID == modelID {
			return cloneModel(m.Model), true
		}
	}
	return ai.Model{}, false
}

// IDs returns each provider and driver ID of the file, sorted.
func (f ModelFile) IDs() []string {
	return slices.Sorted(maps.Keys(f.file))
}

// cloneModel returns m with its own copy of each slice.
func cloneModel(m ai.Model) ai.Model {
	m.Input = slices.Clone(m.Input)
	m.ThinkingLevels = slices.Clone(m.ThinkingLevels)
	m.Pricing.Tiers = slices.Clone(m.Pricing.Tiers)
	return m
}
