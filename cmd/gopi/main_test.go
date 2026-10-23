package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/internal/modelstest"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/anthropic"
	"github.com/sonnes/gopi/pkg/ai/openai"
	"github.com/sonnes/gopi/pkg/ai/openaichat"
	"github.com/sonnes/gopi/pkg/catalog"
	"github.com/sonnes/gopi/pkg/catalog/env"
	"github.com/sonnes/gopi/pkg/harness"
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

func TestNewCatalog(t *testing.T) {
	one := ai.Model{ID: "one", MaxTokens: 100}
	two := ai.Model{ID: "two", MaxTokens: 200}
	next := ai.Model{ID: "vendor/next", Name: "Next"}

	tests := []struct {
		name      string
		spec      string
		detectors []catalog.Detector
		wantSpec  string
		wantModel ai.Model
		wantErr   string
	}{
		{
			name: "no spec gives the first model of the first provider",
			detectors: []catalog.Detector{
				detector("a", true, one, two),
				detector("b", true, two),
			},
			wantSpec:  "a/one",
			wantModel: one,
		},
		{
			name: "no spec skips a provider that is not found",
			detectors: []catalog.Detector{
				detector("a", false, one),
				detector("b", true, two),
			},
			wantSpec:  "b/two",
			wantModel: two,
		},
		{
			name: "no spec skips a provider with no models",
			detectors: []catalog.Detector{
				detector("router", true),
				detector("b", true, two),
			},
			wantSpec:  "b/two",
			wantModel: two,
		},
		{
			name: "a full spec",
			spec: "b/two",
			detectors: []catalog.Detector{
				detector("a", true, one),
				detector("b", true, two),
			},
			wantSpec:  "b/two",
			wantModel: two,
		},
		{
			name: "a bare model ID",
			spec: "two",
			detectors: []catalog.Detector{
				detector("a", true, one),
				detector("b", true, two),
			},
			wantSpec:  "two",
			wantModel: two,
		},
		{
			name: "a model that the provider lists",
			spec: "router/vendor/next",
			detectors: []catalog.Detector{
				listingDetector("router", []ai.Model{next}, nil),
			},
			wantSpec:  "router/vendor/next",
			wantModel: next,
		},
		{
			name: "a model that the provider does not list",
			spec: "router/vendor/nope",
			detectors: []catalog.Detector{
				listingDetector("router", []ai.Model{next}, nil),
			},
			wantErr: `model "router/vendor/nope": catalog: unknown model "router/vendor/nope": provider "router" has no model "vendor/nope": list the models with "gopi models router"`,
		},
		{
			name: "a bare ID that no provider lists",
			spec: "nope",
			detectors: []catalog.Detector{
				listingDetector("router", nil, nil),
			},
			wantErr: `model "nope": catalog: unknown model "nope": list the models with "gopi models"`,
		},
		{
			name: "a provider that gopi does not know",
			spec: "nope/one",
			detectors: []catalog.Detector{
				detector("a", true, one),
			},
			wantErr: `model "nope/one": catalog: unknown model "nope/one": no provider "nope": use one of a`,
		},
		{
			name: "a bare ID that two providers list",
			spec: "shared",
			detectors: []catalog.Detector{
				listingDetector("one", []ai.Model{{ID: "shared"}}, nil),
				listingDetector("two", []ai.Model{{ID: "shared"}}, nil),
			},
			wantErr: `ambiguous model "shared": use one of one/shared, two/shared`,
		},
		{
			name: "a failed list",
			spec: "router/vendor/next",
			detectors: []catalog.Detector{
				listingDetector("router", nil, errors.New("boom")),
			},
			wantErr: "\"gopi models router\"\nboom",
		},
		{
			name: "no provider names each credential",
			detectors: []catalog.Detector{
				detector("a", false, one),
				detector("b", false, two),
			},
			wantErr: "no provider: set A_KEY or B_KEY",
		},
		{
			name: "no spec and no provider with models",
			detectors: []catalog.Detector{
				detector("router", true),
			},
			wantErr: "no default model: give a spec with --model",
		},
		{
			name: "a bare ID with a slash that a found provider serves",
			spec: "a/x",
			detectors: []catalog.Detector{
				detector("a", false, one),
				detector("router", true, ai.Model{ID: "a/x"}),
			},
			wantSpec:  "a/x",
			wantModel: ai.Model{ID: "a/x"},
		},
		{
			name: "a spec of a provider that is not found",
			spec: "a/one",
			detectors: []catalog.Detector{
				detector("a", false, one),
				detector("b", true, two),
			},
			wantErr: `model "a/one": provider "a" has no credential: set A_KEY`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cat, spec, err := newCatalog(t.Context(), tt.spec, tt.detectors)

			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantSpec, spec)
			got, err := cat.LanguageModel(spec)
			require.NoError(t, err)
			assert.Equal(t, tt.wantModel, got.Model)
		})
	}
}

func TestNewCatalog_ListsOnlyTheProviderOfASpec(t *testing.T) {
	next := ai.Model{ID: "vendor/next", Name: "Next"}
	var routerCalls, otherCalls int
	detectors := []catalog.Detector{
		countingDetector("other", nil, errors.New("401"), &otherCalls),
		countingDetector("router", []ai.Model{next}, nil, &routerCalls),
	}

	_, spec, err := newCatalog(t.Context(), "router/vendor/next", detectors)

	require.NoError(t, err)
	assert.Equal(t, "router/vendor/next", spec)
	assert.Equal(t, 1, routerCalls)
	assert.Equal(t, 0, otherCalls, "a full spec lists only its provider")
}

func TestNewCatalog_Env(t *testing.T) {
	tests := []struct {
		name         string
		spec         string
		env          map[string]string
		wantSpec     string
		wantModel    ai.Model
		wantProvider ai.TextProvider
	}{
		{
			name:         "the Anthropic key comes first",
			env:          map[string]string{"ANTHROPIC_API_KEY": "k", "OPENAI_API_KEY": "k"},
			wantSpec:     "anthropic/" + repoFile.Of("anthropic")[0].ID,
			wantModel:    repoFile.Of("anthropic")[0],
			wantProvider: &anthropic.Provider{},
		},
		{
			name:         "the OpenAI key alone picks OpenAI",
			env:          map[string]string{"OPENAI_API_KEY": "k"},
			wantSpec:     "openai/" + repoFile.Of("openai")[0].ID,
			wantModel:    repoFile.Of("openai")[0],
			wantProvider: &openai.Provider{},
		},
		{
			name:         "a known model keeps its prices",
			spec:         "anthropic/" + "claude-opus-5-5",
			env:          map[string]string{"ANTHROPIC_API_KEY": "k"},
			wantSpec:     "anthropic/" + "claude-opus-5-5",
			wantModel:    modelstest.Model("anthropic", "claude-opus-5-5"),
			wantProvider: &anthropic.Provider{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keys := []string{
				"ANTHROPIC_API_KEY",
				"OPENAI_API_KEY",
				"OPENROUTER_API_KEY",
				"FIREWORKS_API_KEY",
			}
			for _, key := range keys {
				t.Setenv(key, tt.env[key])
			}

			cat, spec, err := newCatalog(t.Context(), tt.spec, env.Detectors(repoFile))

			require.NoError(t, err)
			assert.Equal(t, tt.wantSpec, spec)
			lm, err := cat.LanguageModel(spec)
			require.NoError(t, err)
			assert.Equal(t, tt.wantModel, lm.Model)
			assert.IsType(t, tt.wantProvider, lm.Provider)
		})
	}
}

func TestNewTools(t *testing.T) {
	tests := []struct {
		name    string
		names   string
		want    []string
		wantErr string
	}{
		{
			name:  "the default gives every tool",
			names: defaultTools,
			want: []string{
				"read_file",
				"write_file",
				"edit_file",
				"bash",
				"grep",
				"find",
				"todo_write",
				"ask_user_question",
			},
		},
		{
			name:  "a list picks tools in its order",
			names: "grep, read_file",
			want:  []string{"grep", "read_file"},
		},
		{
			name:  "an empty list gives no tool",
			names: "",
		},
		{
			name:    "an unknown tool",
			names:   "read_file,nope",
			wantErr: `unknown tool "nope"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tools, err := newTools(t.TempDir(), tt.names, true, terminal{})

			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			var got []string
			for _, tl := range tools {
				got = append(got, tl.Info().Name)
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNewTools_Fence(t *testing.T) {
	tests := []struct {
		name    string
		fenced  bool
		isError bool
	}{
		{
			name:    "the fence keeps .git/config read-only",
			fenced:  true,
			isError: true,
		},
		{
			name:   "without the fence, the tools write anywhere",
			fenced: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0o755))
			tools, err := newTools(root, "write_file", tt.fenced, terminal{})
			require.NoError(t, err)

			res, err := tools[0].Run(t.Context(), ai.ToolCall{
				ID:    "call_1",
				Name:  "write_file",
				Input: []byte(`{"path":".git/config","content":"[core]"}`),
			})

			require.NoError(t, err)
			assert.Equal(t, tt.isError, res.IsError)
		})
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

func TestStoredSpec(t *testing.T) {
	dir := t.TempDir()
	cat := catalog.New()
	cat.RegisterTextProvider("fake", &openaichat.Provider{}, ai.Model{ID: "one"}, ai.Model{ID: "two"})
	opts := []harness.Option{
		harness.Catalog(cat),
		harness.Builtin(harness.NoBuiltins),
		harness.Sources(),
	}

	none, err := storedSpec(t.Context(), dir, "")
	require.NoError(t, err)
	assert.Empty(t, none, "no session ID")

	missing, err := storedSpec(t.Context(), dir, "ada")
	require.NoError(t, err)
	assert.Empty(t, missing, "a session that does not exist")

	_, err = newAgent(t.Context(), opts, "fake/one", "ada", dir)
	require.NoError(t, err)
	stored, err := storedSpec(t.Context(), dir, "ada")
	require.NoError(t, err)
	assert.Equal(t, "fake/one", stored)

	_, err = newAgent(t.Context(), opts, "fake/two", "ada", dir)
	require.NoError(t, err)
	changed, err := storedSpec(t.Context(), dir, "ada")
	require.NoError(t, err)
	assert.Equal(t, "fake/two", changed, "a spec on a resume changes the model of the session")
}

func TestNewAgentKeepsASession(t *testing.T) {
	dir := t.TempDir()
	cat := catalog.New()
	cat.RegisterTextProvider("fake", &openaichat.Provider{}, ai.Model{ID: "test"})
	opts := []harness.Option{
		harness.Catalog(cat),
		harness.Model("fake/test"),
		harness.Builtin(harness.NoBuiltins),
		harness.Sources(),
	}

	a, err := newAgent(t.Context(), opts, "fake/test", "ada", dir)
	require.NoError(t, err)
	assert.Equal(t, "ada", a.Log().Session().ID)
	assert.FileExists(t, filepath.Join(dir, "ada.jsonl"))

	b, err := newAgent(t.Context(), opts, "fake/test", "", dir)
	require.NoError(t, err)
	assert.Empty(t, b.Log().Session().ID, "with no session ID, the agent keeps no session")
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
