package catalog_test

import (
	"fmt"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/catalog"
)

// A spec resolves in three forms. A bare ID resolves when one provider
// registered it. An ID that the provider did not register passes
// through with no metadata.
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
			panic(err)
		}
		fmt.Printf("%s: %s, max tokens %d\n", spec, m.Name, m.MaxTokens)
	}
	// Output:
	// one/alpha: Alpha, max tokens 100
	// alpha: Alpha, max tokens 100
	// one/next: next, max tokens 0
}
