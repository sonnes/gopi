// Command modelsync updates models.json, the default models of each
// provider and each driver of gopi. Run it with `make models`.
//
// The IDs are the IDs of the detectors of package env. For each ID that
// the file has, or that -ids names, it lists the models of the provider
// or the driver, and merges the list into the file with the rule of
// catalog.MergeModel. Then it fills each empty value from
// https://models.dev/api.json with catalog.FillModel. A price in the
// file always stays. A model that a list leaves out stays,
// unless -prune is set. The report names each change, each source that
// it skipped, and each price that differs from models.dev.
//
// A provider lists its models with its key, and a driver lists the
// models of its CLI on the PATH. The list of OpenRouter and models.dev
// need no key. The CLI of Claude Code runs the hooks of its settings.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/sonnes/gopi/internal/modelsfile"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/openrouter"
	"github.com/sonnes/gopi/pkg/catalog"
	"github.com/sonnes/gopi/pkg/catalog/env"
)

// devURL is the catalog of models.dev.
const devURL = "https://models.dev/api.json"

// fetchTimeout is the longest time for the lists and for models.dev.
const fetchTimeout = 2 * time.Minute

// maxDevFile caps the size of the catalog of models.dev. The catalog is
// near 400 KB, so a larger body is not the catalog.
const maxDevFile = 64 << 20

func main() {
	path := flag.String("file", "models.json", "the path of models.json")
	ids := flag.String("ids", "", "the provider and driver IDs to update, with commas. Empty updates each ID of the file")
	prune := flag.Bool("prune", false, "remove a model that its list leaves out")
	flag.Parse()

	if err := run(*path, *ids, *prune); err != nil {
		fmt.Fprintln(os.Stderr, "modelsync:", err)
		os.Exit(1)
	}
}

func run(path, ids string, prune bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
	defer cancel()

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	f, err := modelsfile.Parse(data)
	if err != nil {
		return err
	}
	sources, err := selectSources(allSources(), f, ids)
	if err != nil {
		return err
	}
	dev, err := fetchDev(ctx)
	if err != nil {
		return err
	}

	next, report := syncFile(ctx, f, sources, dev, prune)
	out, err := modelsfile.Marshal(next)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return err
	}
	for _, line := range report {
		fmt.Println(line)
	}
	return nil
}

// selectSources returns the sources that ids names, in the order of all.
// An empty ids selects each source that f has an entry for, so a
// detector with no models in the file, such as openaichat, stays out.
// To add the entry of a new ID, name it in ids. An ID that all does not
// have is an error, so a typo does not rewrite the file with no change.
func selectSources(all []source, f modelsfile.File, ids string) ([]source, error) {
	if strings.TrimSpace(ids) == "" {
		var out []source
		for _, s := range all {
			if _, ok := f[s.ID]; ok {
				out = append(out, s)
			}
		}
		return out, nil
	}
	known := make([]string, 0, len(all))
	for _, s := range all {
		known = append(known, s.ID)
	}
	var names []string
	for id := range strings.SplitSeq(ids, ",") {
		id = strings.TrimSpace(id)
		if !slices.Contains(known, id) {
			return nil, fmt.Errorf("no source %q: use one of %s", id, strings.Join(known, ", "))
		}
		names = append(names, id)
	}
	var out []source
	for _, s := range all {
		if slices.Contains(names, s.ID) {
			out = append(out, s)
		}
	}
	return out, nil
}

// rules returns, by ID, the fields of a source that its detector does
// not know. If a rule sets Skip, the generator does not call the list.
func rules() map[string]source {
	return map[string]source{
		"anthropic": {Dev: "anthropic"},
		"openai": {
			Dev:       "openai",
			DevAdmits: true,
		},
		// The model list of OpenRouter is public, so a list needs no key.
		"openrouter": {List: openrouter.New(openrouter.APIKey("")).ListModels},
		"fireworks": {
			Skip:    "models.dev is the source, because the list of Fireworks has no prices",
			Dev:     "fireworks-ai",
			DevAdds: true,
		},
	}
}

// allSources returns a source for each ID of the detectors of package
// env, in the order of the detectors. The detect loop of the catalog
// picks the provider or the driver of each ID.
func allSources() []source {
	lists := make(map[string]listFunc)
	providers := env.Detectors(catalog.ModelFile{})
	for i, d := range providers {
		providers[i].Detect = keepList(d.Provider, d.Detect, lists)
	}
	drivers := env.DriverDetectors(catalog.ModelFile{})
	for i, d := range drivers {
		drivers[i].Detect = keepList(d.Provider, d.Detect, lists)
	}
	cat := catalog.New()
	cat.Detect(providers...)
	cat.DetectDrivers(drivers...)

	byID := rules()
	var sources []source
	add := func(id, skip string, driver bool) {
		if slices.ContainsFunc(sources, func(s source) bool { return s.ID == id }) {
			return
		}
		s := byID[id]
		s.ID = id
		s.Driver = driver
		if s.Skip == "" {
			s.Skip = skip
			if l, ok := lists[id]; ok {
				s.List = l
			}
		}
		sources = append(sources, s)
	}
	for _, d := range providers {
		add(d.Provider, "set "+d.Source, false)
	}
	for _, d := range drivers {
		add(d.Provider, "put the CLI on the PATH: "+d.Source, true)
	}
	return sources
}

// listFunc lists the models of one provider or driver.
type listFunc = func(context.Context) ([]ai.Model, error)

// keepList wraps detect. If the value that detect builds lists its
// models, keepList puts the list in lists under id.
func keepList[T any](id string, detect func() (T, bool), lists map[string]listFunc) func() (T, bool) {
	return func() (T, bool) {
		v, ok := detect()
		if l, isLister := any(v).(ai.ModelLister); ok && isLister {
			lists[id] = l.ListModels
		}
		return v, ok
	}
}

// fetchDev returns the catalog of models.dev.
func fetchDev(ctx context.Context) (devCatalog, error) {
	body, err := modelsfile.Get(ctx, devURL, maxDevFile)
	if err != nil {
		return nil, fmt.Errorf("models.dev: %w", err)
	}
	return parseDev(body)
}
