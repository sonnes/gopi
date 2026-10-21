package env_test

import (
	"context"
	"fmt"
	"os"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/anthropic"
	"github.com/sonnes/gopi/pkg/catalog"
	"github.com/sonnes/gopi/pkg/catalog/env"
	"github.com/sonnes/gopi/pkg/harness"
)

// Resolve a spec with the built-in detectors, and give the model to an
// agent.
func Example() {
	c := catalog.New()
	c.Detect(env.Detectors()...)

	lm, err := c.LanguageModel("claude-sonnet-5")
	if err != nil {
		panic(err)
	}
	a := agent.New(lm, agent.System(func(context.Context) ([]ai.Section, error) {
		return []ai.Section{{Name: "base", Text: "Answer in one sentence."}}, nil
	}))
	_ = a
}

// Put a detector before the built-in chain to give its source priority.
// For each provider ID, the first detector that finds a credential wins.
func ExampleDetectors() {
	vault := catalog.Detector{
		Provider: "anthropic",
		Source:   "VAULT_ANTHROPIC_KEY",
		Models:   anthropic.Models(),
		Detect: func() (ai.TextProvider, bool) {
			key := os.Getenv("VAULT_ANTHROPIC_KEY")
			return anthropic.New(anthropic.APIKey(key)), key != ""
		},
	}

	c := catalog.New()
	c.Detect(append([]catalog.Detector{vault}, env.Detectors()...)...)
}

// Ask each provider for the models that its key can call.
func Example_fetchModels() {
	ctx := context.Background()
	c := catalog.New()
	c.Detect(env.Detectors()...)

	for _, r := range c.FetchModels(ctx) {
		if r.Err != nil {
			fmt.Printf("%s: %v\n", r.Provider, r.Err)
			continue
		}
		fmt.Printf("%s lists %d models\n", r.Provider, r.Models)
	}
}

// Detect the agent CLIs next to the providers. One spec then names the
// kind of agent and its model. A spec of a provider runs on the
// default driver, the harness.
func ExampleDriverDetectors() {
	ctx := context.Background()
	c := catalog.New()
	c.Detect(env.Detectors()...)
	c.DetectDrivers(env.DriverDetectors()...)
	c.DefaultDriver(harness.New(harness.Catalog(c)))

	// Optional: add the models of the installed CLIs to the registered
	// models. Each list starts its CLI.
	c.FetchDriverModels(ctx)

	spec := "claude-code/opus"
	d, model, err := c.Driver(spec)
	if err != nil {
		panic(err)
	}
	runner, err := d.Start(ctx, agent.Config{
		Session: "fix-login",
		Spec:    spec,
		Model:   model,
	})
	if err != nil {
		panic(err)
	}
	_ = runner
}
