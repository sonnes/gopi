package catalog

import "github.com/sonnes/gopi/pkg/ai"

// Detector builds a provider from one credential source, such as an
// environment variable. A provider package has no detection of its own,
// so a detector is where the credentials of a process meet the catalog.
type Detector struct {
	// Provider is the ID that the catalog registers the provider under.
	Provider string
	// Source names the credential, for example "ANTHROPIC_API_KEY". It
	// is for a person to read.
	Source string
	// Models are the models that the catalog registers with the
	// provider.
	Models []ai.Model
	// Detect builds the provider. If the credential is absent, it
	// returns false.
	Detect func() (ai.TextProvider, bool)
}

// Detection is one detector that found its credential.
type Detection struct {
	Provider string
	Source   string
}

// Detect runs ds in order and registers each provider that a detector
// builds. It registers the provider for images and for speech too, if
// the provider implements [ai.ImageProvider] or [ai.SpeechProvider].
//
// Order is precedence. If two detectors have the same Provider, the
// first one that finds its credential wins, and Detect skips the rest.
// To give a source priority, put its detector first.
func (c *Catalog) Detect(ds ...Detector) []Detection {
	var found []Detection
	done := make(map[string]bool)
	for _, d := range ds {
		if done[d.Provider] {
			continue
		}
		p, ok := d.Detect()
		if !ok {
			continue
		}
		done[d.Provider] = true
		c.register(d.Provider, p, d.Models)
		found = append(found, Detection{
			Provider: d.Provider,
			Source:   d.Source,
		})
	}
	return found
}

// register registers p for each capability that it implements.
func (c *Catalog) register(id string, p ai.TextProvider, models []ai.Model) {
	c.RegisterTextProvider(id, p, models...)
	if ip, ok := p.(ai.ImageProvider); ok {
		c.RegisterImageProvider(id, ip)
	}
	if sp, ok := p.(ai.SpeechProvider); ok {
		c.RegisterSpeechProvider(id, sp)
	}
}
