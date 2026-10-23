// Command modelsync updates models.json, the default models of each
// provider and each driver of gopi. Run it with `make models`.
//
// For each ID, it lists the models of the provider or the driver,
// merges the list into the file with the rule of catalog.MergeModel,
// and fills each empty value from https://models.dev/api.json. A price
// in the file always stays. A model that a list leaves out stays,
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
	"io"
	"net/http"
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

func main() {
	path := flag.String("file", "models.json", "the path of models.json")
	ids := flag.String("ids", "", "the provider and driver IDs to update, with commas. Empty updates each ID")
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
	sources, err := selectSources(allSources(), ids)
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
// An empty ids selects each source. An ID that all does not have is an
// error, so a typo does not rewrite the file with no change.
func selectSources(all []source, ids string) ([]source, error) {
	if strings.TrimSpace(ids) == "" {
		return all, nil
	}
	known := make([]string, 0, len(all))
	for _, s := range all {
		known = append(known, s.ID)
	}
	var names []string
	for _, id := range strings.Split(ids, ",") {
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

// allSources returns the source of each provider and driver of
// models.json, from the detectors of package env.
func allSources() []source {
	providers := make(map[string]ai.TextProvider)
	for _, d := range env.Detectors(catalog.ModelFile{}) {
		if _, done := providers[d.Provider]; done {
			continue
		}
		if p, ok := d.Detect(); ok {
			providers[d.Provider] = p
		}
	}
	provider := func(id, skip string) source {
		s := source{ID: id, Kind: modelsfile.KindProvider, Skip: skip}
		if l, ok := providers[id].(ai.ModelLister); ok {
			s.List = l.ListModels
		}
		return s
	}

	anthropic := provider("anthropic", "set ANTHROPIC_API_KEY")
	anthropic.Dev = "anthropic"

	openai := provider("openai", "set OPENAI_API_KEY")
	openai.Dev = "openai"
	openai.DevAdmits = true

	// The model list of OpenRouter is public, so a list needs no key.
	router := provider("openrouter", "")
	if router.List == nil {
		router.List = openrouter.New(openrouter.APIKey("")).ListModels
	}

	fireworks := source{
		ID:      "fireworks",
		Kind:    modelsfile.KindProvider,
		Skip:    "models.dev is the source, because the list of Fireworks has no prices",
		Dev:     "fireworks-ai",
		DevAdds: true,
	}

	sources := []source{anthropic, openai, router, fireworks}
	for _, d := range env.DriverDetectors(catalog.ModelFile{}) {
		s := source{
			ID:   d.Provider,
			Kind: modelsfile.KindDriver,
			Skip: "put the CLI on the PATH: " + d.Source,
		}
		if drv, ok := d.Detect(); ok {
			if l, ok := drv.(ai.ModelLister); ok {
				s.List = l.ListModels
			}
		}
		sources = append(sources, s)
	}
	return sources
}

// fetchDev returns the catalog of models.dev.
func fetchDev(ctx context.Context) (devCatalog, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, devURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("models.dev: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("models.dev: %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("models.dev: %w", err)
	}
	return parseDev(body)
}
