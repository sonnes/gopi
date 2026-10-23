// Package modelstest gives a test the models of models.json in this
// repository. gopi does not embed the file, so a test reads it from
// disk. A test sets its models in package variables, so each function
// panics if it cannot read a model, and the test fails at its start.
package modelstest

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/catalog"
)

// File returns models.json of this repository.
func File() catalog.ModelFile {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("modelstest: find the path of this file")
	}
	path := filepath.Join(filepath.Dir(file), "..", "..", "models.json")
	data, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	f, err := catalog.ParseModelFile(data)
	if err != nil {
		panic(err)
	}
	return f
}

// Model returns the model modelID of the provider or driver id, with
// its limits and prices. A recorded test sends the full model, so its
// request matches the recording.
func Model(id, modelID string) ai.Model {
	m, ok := File().Lookup(id, modelID)
	if !ok {
		panic(fmt.Sprintf("modelstest: no model %q of %q in models.json", modelID, id))
	}
	return m
}
