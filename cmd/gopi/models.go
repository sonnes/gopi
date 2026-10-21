package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/sonnes/gopi/pkg/catalog"
	"github.com/sonnes/gopi/pkg/catalog/env"
)

// modelsCommand returns the "gopi models" command. It lists the spec of
// each model that --model takes: the registered models of each provider
// that gopi finds, and the models that each provider lists.
func modelsCommand() *cli.Command {
	return &cli.Command{
		Name:      "models",
		Usage:     "list the model specs of the providers that gopi finds. Each provider lists its models from its API",
		ArgsUsage: "[provider...]",
		Action: func(ctx context.Context, c *cli.Command) error {
			return printModels(ctx, c.Root().Writer, c.Root().ErrWriter, env.Detectors(), c.Args().Slice())
		},
	}
}

// printModels writes the spec of each model of providers to out, one
// per line, sorted. With no providers, it writes the models of each
// provider that ds find. The error of a list goes to errOut, and the
// other lists go on.
func printModels(
	ctx context.Context,
	out io.Writer,
	errOut io.Writer,
	ds []catalog.Detector,
	providers []string,
) error {
	cat := catalog.New()
	found := cat.Detect(ds...)
	if len(found) == 0 {
		return fmt.Errorf("no provider: set %s", sources(ds))
	}
	for _, id := range providers {
		if err := providerErr(ds, found, id); err != nil {
			return err
		}
	}

	for _, r := range cat.FetchModels(ctx, providers...) {
		if r.Err != nil {
			fmt.Fprintf(errOut, "gopi: %v\n", r.Err)
		}
	}
	for _, spec := range cat.Specs() {
		id, _, _ := strings.Cut(spec, "/")
		if len(providers) > 0 && !slices.Contains(providers, id) {
			continue
		}
		fmt.Fprintln(out, spec)
	}
	return nil
}

// providerErr returns an error if found does not have the provider id.
// The error names the credential of a provider that ds know, or lists
// the providers of ds.
func providerErr(ds []catalog.Detector, found []catalog.Detection, id string) error {
	isFound := slices.ContainsFunc(found, func(d catalog.Detection) bool {
		return d.Provider == id
	})
	if isFound {
		return nil
	}
	var own []catalog.Detector
	known := make(map[string]bool)
	for _, d := range ds {
		known[d.Provider] = true
		if d.Provider == id {
			own = append(own, d)
		}
	}
	if len(own) > 0 {
		return fmt.Errorf("provider %q has no credential: set %s", id, sources(own))
	}
	return fmt.Errorf("no provider %q: use one of %s", id, strings.Join(slices.Sorted(maps.Keys(known)), ", "))
}

// withHint adds to err the command that lists the models, if err is an
// unknown model of a known provider or an unknown bare model ID.
func withHint(err error) error {
	var unknown *catalog.UnknownModelError
	if !errors.As(err, &unknown) || len(unknown.Providers) > 0 {
		return err
	}
	command := "gopi models"
	if unknown.Provider != "" {
		command += " " + unknown.Provider
	}
	return fmt.Errorf("%w: list the models with %q", err, command)
}
