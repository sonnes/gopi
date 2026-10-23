package catalog_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/internal/modelsfile"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/catalog"
)

// repoFile is models.json of this repository, the file that make models
// writes.
const repoFile = "../../models.json"

// smallFile is a file in the format of models.json, with a field that
// this version does not know.
const smallFile = `{
  "codex": {
    "kind": "driver",
    "default": "b",
    "new_field": true,
    "models": [{"id": "a", "name": "A"}, {"id": "b", "name": "B"}]
  }
}`

// knownLevels and knownInput are the values of the ai package that the
// file can hold.
var (
	knownLevels = []ai.ThinkingLevel{
		ai.ThinkingOff,
		ai.ThinkingLow,
		ai.ThinkingMedium,
		ai.ThinkingHigh,
		ai.ThinkingXHigh,
		ai.ThinkingMax,
	}
	knownInput = []ai.Modality{ai.ModalityText, ai.ModalityImage, ai.ModalityPDF}
)

func TestReadModelFile_Path(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	require.NoError(t, os.WriteFile(path, []byte(smallFile), 0o600))

	f, err := catalog.ReadModelFile(t.Context(), path)

	require.NoError(t, err)
	assert.Equal(t, []string{"b", "a"}, ids(f.Of("codex")), "the default model comes first")
}

func TestReadModelFile_URL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(smallFile))
	}))
	t.Cleanup(srv.Close)

	f, err := catalog.ReadModelFile(t.Context(), srv.URL+"/models.json")

	require.NoError(t, err)
	assert.Equal(t, []string{"b", "a"}, ids(f.Of("codex")))
}

func TestReadModelFile_Errors(t *testing.T) {
	notFound := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(notFound.Close)
	broken := filepath.Join(t.TempDir(), "broken.json")
	require.NoError(t, os.WriteFile(broken, []byte("{"), 0o600))

	tests := []struct {
		name string
		src  string
	}{
		{name: "a missing file", src: filepath.Join(t.TempDir(), "nope.json")},
		{name: "a URL that fails", src: notFound.URL},
		{name: "a file that does not parse", src: broken},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := catalog.ReadModelFile(t.Context(), tt.src)

			assert.Error(t, err)
			assert.Empty(t, f.IDs())
		})
	}
}

func TestModelFile_OfAndLookup(t *testing.T) {
	f, err := catalog.ParseModelFile([]byte(smallFile))
	require.NoError(t, err)

	got, ok := f.Lookup("codex", "a")
	assert.True(t, ok)
	assert.Equal(t, ai.Model{ID: "a", Name: "A"}, got)
	_, ok = f.Lookup("codex", "nope")
	assert.False(t, ok)
	assert.Nil(t, f.Of("nope"))
	assert.Equal(t, []string{"codex"}, f.IDs())

	ms := f.Of("codex")
	ms[0].ID = "changed"
	assert.Equal(t, "b", f.Of("codex")[0].ID, "each call returns a copy")
}

func TestModelFile_MarshalJSON(t *testing.T) {
	f, err := catalog.ParseModelFile([]byte(smallFile))
	require.NoError(t, err)

	data, err := json.Marshal(f)
	require.NoError(t, err)
	again, err := catalog.ParseModelFile(data)

	require.NoError(t, err)
	assert.Equal(t, f.Of("codex"), again.Of("codex"), "a program can write the file that it read")
}

func TestModelFile_Zero(t *testing.T) {
	var f catalog.ModelFile

	assert.Nil(t, f.Of("codex"))
	assert.Empty(t, f.IDs())
}

// TestRepoFile checks the rules of models.json that the generator and a
// person who edits the file must keep.
func TestRepoFile(t *testing.T) {
	data, err := os.ReadFile(repoFile)
	require.NoError(t, err)
	file, err := modelsfile.Parse(data)
	require.NoError(t, err, "the generator writes no field that it does not know")
	require.NotEmpty(t, file)

	for id, e := range file {
		t.Run(id, func(t *testing.T) {
			assert.Contains(t, []string{modelsfile.KindProvider, modelsfile.KindDriver}, e.Kind)
			require.NotEmpty(t, e.Models)

			got := make([]string, len(e.Models))
			for i, m := range e.Models {
				got[i] = m.ID
			}
			assert.True(t, slices.IsSorted(got), "the models are sorted by ID")
			assert.Len(t, slices.Compact(slices.Clone(got)), len(got), "each ID is unique")
			assert.Contains(t, got, e.Default, "the default is a model of the list")

			for _, m := range e.Models {
				assert.NotEmpty(t, m.Name, m.ID)
				for _, level := range m.ThinkingLevels {
					assert.Contains(t, knownLevels, level, m.ID)
				}
				for _, mod := range m.Input {
					assert.Contains(t, knownInput, mod, m.ID)
				}
				checkPricing(t, m.ID, m.Pricing)
				checkLimits(t, m.Model)
			}
		})
	}
}

// checkLimits checks that the output cap and each price tier of m fit
// in its context window. A call with no MaxTokens option sends the
// output cap, and an API rejects a cap that is larger than the window.
func checkLimits(t *testing.T, m ai.Model) {
	t.Helper()
	if m.ContextWindow == 0 {
		return
	}
	assert.LessOrEqual(t, m.MaxTokens, m.ContextWindow, "%s: the output cap fits in the context window", m.ID)
	for _, tier := range m.Pricing.Tiers {
		assert.Less(t, tier.InputAbove, m.ContextWindow, "%s: a request can reach the tier", m.ID)
	}
}

// checkPricing checks that no price of p is negative, and that its
// tiers are in order of InputAbove.
func checkPricing(t *testing.T, id string, p ai.Pricing) {
	t.Helper()
	prices := []float64{p.Input, p.Output, p.CacheRead, p.CacheWrite, p.CacheWriteLong}
	for _, tier := range p.Tiers {
		prices = append(prices, tier.Input, tier.Output, tier.CacheRead, tier.CacheWrite, tier.CacheWriteLong)
	}
	for _, price := range prices {
		assert.GreaterOrEqual(t, price, 0.0, id)
	}
	sorted := slices.IsSortedFunc(p.Tiers, func(a, b ai.PriceTier) int {
		return a.InputAbove - b.InputAbove
	})
	assert.True(t, sorted, "the tiers of %s are in order of InputAbove", id)
}

// ids returns the ID of each model of ms.
func ids(ms []ai.Model) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.ID
	}
	return out
}
