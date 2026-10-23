package modelsfile_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/internal/modelsfile"
	gopiai "github.com/sonnes/gopi/pkg/ai"
)

func TestParse_RejectsUnknownFields(t *testing.T) {
	_, err := modelsfile.Parse([]byte(`{"x":{"kind":"provider","default":"a","models":[{"id":"a","name":"A","price":1}]}}`))

	require.ErrorContains(t, err, `unknown field "price"`)
}

// TestMarshal_RoundTrip checks that Marshal writes models.json in the
// form that the file has, so a generated file differs from the old one
// only where a value changed.
func TestMarshal_RoundTrip(t *testing.T) {
	data, err := os.ReadFile("../../models.json")
	require.NoError(t, err)
	f, err := modelsfile.Parse(data)
	require.NoError(t, err)

	got, err := modelsfile.Marshal(f)

	require.NoError(t, err)
	assert.Equal(t, string(data), string(got))
}

func TestMarshal_SortsModels(t *testing.T) {
	f := modelsfile.File{"x": {
		Kind:    modelsfile.KindProvider,
		Default: "b",
		Models: []modelsfile.Model{
			{Model: ai("b")},
			{Model: ai("a")},
		},
	}}

	got, err := modelsfile.Marshal(f)
	require.NoError(t, err)
	back, err := modelsfile.Parse(got)
	require.NoError(t, err)

	assert.Equal(t, "a", back["x"].Models[0].ID)
	assert.Equal(t, "b", back["x"].Models[1].ID)
}

// ai returns a model with the ID and the name id.
func ai(id string) gopiai.Model { return gopiai.Model{ID: id, Name: id} }
