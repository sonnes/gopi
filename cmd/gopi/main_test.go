package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/internal/modelstest"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/openaichat"
	"github.com/sonnes/gopi/pkg/catalog"
)

// lister is a text provider that lists its models, or fails. It counts
// its lists in calls, if calls is not nil.
type lister struct {
	*openaichat.Provider
	models []ai.Model
	err    error
	calls  *int
}

func (l lister) ListModels(context.Context) ([]ai.Model, error) {
	if l.calls != nil {
		*l.calls++
	}
	return l.models, l.err
}

// repoFile is models.json of this repository.
var repoFile = modelstest.File()

// detector returns a detector of the provider id with models. It finds
// its credential if found is true. Its provider lists no models.
func detector(id string, found bool, models ...ai.Model) catalog.Detector {
	return catalog.Detector{
		Provider: id,
		Source:   strings.ToUpper(id) + "_KEY",
		Models:   models,
		Detect: func() (ai.TextProvider, bool) {
			return lister{}, found
		},
	}
}

// listingDetector returns a detector of the provider id that registers
// no models. Its provider lists listed, or fails with err.
func listingDetector(id string, listed []ai.Model, err error) catalog.Detector {
	return countingDetector(id, listed, err, nil)
}

// countingDetector is a listingDetector that counts the lists of its
// provider in calls.
func countingDetector(id string, listed []ai.Model, err error, calls *int) catalog.Detector {
	return catalog.Detector{
		Provider: id,
		Source:   strings.ToUpper(id) + "_KEY",
		Detect: func() (ai.TextProvider, bool) {
			p := lister{
				models: listed,
				err:    err,
				calls:  calls,
			}
			return p, true
		},
	}
}

func TestFenceError(t *testing.T) {
	err := fenceError(fmt.Errorf("sandbox: fence: no backend for linux: %w", errors.ErrUnsupported))

	require.ErrorIs(t, err, errors.ErrUnsupported)
	assert.ErrorContains(t, err, "--fence=false")
	assert.Equal(t, errors.New("boom"), fenceError(errors.New("boom")))
}

func TestSessionsDir(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "GOPI_HOME",
			env:  map[string]string{"GOPI_HOME": "/data/gopi", "HOME": "/home/ada"},
			want: "/data/gopi/sessions",
		},
		{
			name: "the home of the user",
			env:  map[string]string{"HOME": "/home/ada"},
			want: "/home/ada/.gopi/sessions",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(k string) string { return tt.env[k] }
			got, err := sessionsDir(getenv)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestModelsSource(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "GOPI_MODELS",
			env:  map[string]string{"GOPI_MODELS": "https://example.com/models.json", "GOPI_HOME": "/data/gopi"},
			want: "https://example.com/models.json",
		},
		{
			name: "GOPI_HOME",
			env:  map[string]string{"GOPI_HOME": "/data/gopi", "HOME": "/home/ada"},
			want: "/data/gopi/models.json",
		},
		{
			name: "the home of the user",
			env:  map[string]string{"HOME": "/home/ada"},
			want: "/home/ada/.gopi/models.json",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(k string) string { return tt.env[k] }
			got, err := modelsSource(getenv)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPrintModels(t *testing.T) {
	alpha := ai.Model{ID: "alpha"}
	listed := ai.Model{ID: "vendor/next"}
	detectors := []catalog.Detector{
		detector("a", true, alpha),
		countingDetector("router", []ai.Model{listed}, nil, nil),
		countingDetector("broken", nil, errors.New("broken: 401 Unauthorized"), nil),
		detector("missing", false, alpha),
	}

	tests := []struct {
		name       string
		providers  []string
		wantOut    string
		wantErrOut string
		wantErr    string
	}{
		{
			name:       "every provider",
			wantOut:    "a/alpha\nrouter/vendor/next\n",
			wantErrOut: "gopi: broken: 401 Unauthorized\n",
		},
		{
			name:      "one provider",
			providers: []string{"router"},
			wantOut:   "router/vendor/next\n",
		},
		{
			name:      "a provider with no credential",
			providers: []string{"missing"},
			wantErr:   `provider "missing" has no credential: set MISSING_KEY`,
		},
		{
			name:      "a provider that gopi does not know",
			providers: []string{"nope"},
			wantErr:   `no provider "nope": use one of a, broken, missing, router`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut strings.Builder

			err := printModels(t.Context(), &out, &errOut, detectors, tt.providers)

			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantOut, out.String())
			assert.Equal(t, tt.wantErrOut, errOut.String())
		})
	}
}

func TestDefDirs(t *testing.T) {
	tests := []struct {
		name string
		dirs []string
		home string
		want []string
	}{
		{
			name: "the flags",
			dirs: []string{"/a", "/b"},
			home: "/home/ada",
			want: []string{"/a", "/b"},
		},
		{
			name: "no flag",
			home: "/home/ada",
			want: []string{"/home/ada/.claude", "/repo/.claude", "/repo"},
		},
		{
			name: "no flag and no home folder",
			want: []string{"/repo/.claude", "/repo"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := options{root: "/repo", dirs: tt.dirs}

			assert.Equal(t, tt.want, defDirs(o, tt.home))
		})
	}
}
