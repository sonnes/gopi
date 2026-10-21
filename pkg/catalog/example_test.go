package catalog_test

import (
	"fmt"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/catalog"
)

// A spec names a registered model. A bare ID resolves when one provider
// registered it.
func ExampleCatalog_Model() {
	c := catalog.New()
	c.RegisterTextProvider(
		"one",
		textProvider{},
		ai.Model{ID: "alpha", Name: "Alpha", MaxTokens: 100},
	)

	for _, spec := range []string{"one/alpha", "alpha", "one/next"} {
		m, err := c.Model(spec)
		if err != nil {
			fmt.Println(err)
			continue
		}
		fmt.Printf("%s: %s, max tokens %d\n", spec, m.Name, m.MaxTokens)
	}
	// Output:
	// one/alpha: Alpha, max tokens 100
	// alpha: Alpha, max tokens 100
	// catalog: unknown model "one/next": provider "one" has no model "next"
}
