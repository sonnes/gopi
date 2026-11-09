package modelsfile_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/internal/modelsfile"
	gopiai "github.com/sonnes/gopi/pkg/ai"
)

func TestParse_RejectsUnknownFields(t *testing.T) {
	_, err := modelsfile.Parse([]byte(`{"x":{"default":"a","models":[{"id":"a","name":"A","price":1}]}}`))

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

func TestGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("12345"))
	}))
	defer srv.Close()

	got, err := modelsfile.Get(t.Context(), srv.URL, 5)
	require.NoError(t, err)
	assert.Equal(t, "12345", string(got))

	_, err = modelsfile.Get(t.Context(), srv.URL, 4)
	require.ErrorIs(t, err, modelsfile.ErrTooLarge, "a cut body is an error, not a shorter file")

	_, err = modelsfile.Get(t.Context(), srv.URL+"/missing", 5)
	require.ErrorContains(t, err, "404 Not Found")
}

// ai returns a model with the ID and the name id.
func ai(id string) gopiai.Model { return gopiai.Model{ID: id, Name: id} }
