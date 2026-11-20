package catalog

import (
	"cmp"
	"errors"
	"fmt"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
)

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
	// provider. The first model is the default of the provider: a
	// caller that names no model, such as the CLI, uses it.
	// [ModelFile.Of] returns the models of models.json in this order.
	Models []ai.Model
	// Detect builds the provider. If the credential is absent, it
	// returns false. The provider implements one or more of the
	// capabilities that [Catalog.RegisterProvider] takes.
	Detect func() (any, bool)
}

// DriverDetector builds a driver from one source, such as a CLI on the
// PATH. It is the [Detector] of a driver.
type DriverDetector struct {
	// Provider is the ID that the catalog registers the driver under.
	Provider string
	// Source names the source, for example "claude on PATH". It is for
	// a person to read.
	Source string
	// Models are the models that the catalog registers with the
	// driver. The first model is the default of the driver.
	Models []ai.Model
	// Detect builds the driver. If the source is absent, it returns
	// false.
	Detect func() (agent.Driver, bool)
}

// Detection is one detector that found its source. Provider is the ID
// of the provider or of the driver.
type Detection struct {
	Provider string
	Source   string
}

// Detect runs ds in order and registers each provider that a detector
// builds, with [Catalog.RegisterProvider].
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
		if err := c.RegisterProvider(d.Provider, p, d.Models...); err != nil {
			continue
		}
		found = append(found, Detection{
			Provider: d.Provider,
			Source:   d.Source,
		})
	}
	return found
}

// DetectDrivers runs ds in order and registers each driver that a
// detector builds, with the models of the detector. Order is
// precedence, as in [Catalog.Detect].
func (c *Catalog) DetectDrivers(ds ...DriverDetector) []Detection {
	var found []Detection
	done := make(map[string]bool)
	for _, d := range ds {
		if done[d.Provider] {
			continue
		}
		drv, ok := d.Detect()
		if !ok {
			continue
		}
		done[d.Provider] = true
		c.RegisterDriver(d.Provider, drv, d.Models...)
		found = append(found, Detection{
			Provider: d.Provider,
			Source:   d.Source,
		})
	}
	return found
}

// RegisterProvider registers p under id for each capability that it
// implements: [ai.TextProvider], [ai.ImageProvider],
// [ai.DecisionProvider], [ai.SpeechProvider], and
// [ai.TranscriptionProvider]. It keeps a capability of id that p does
// not implement. It adds models to the model index. If p
// implements none of them, it registers nothing and the error wraps
// [errors.ErrUnsupported].
func (c *Catalog) RegisterProvider(id string, p any, models ...ai.Model) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var e providers
	e.text, _ = p.(ai.TextProvider)
	e.image, _ = p.(ai.ImageProvider)
	e.decision, _ = p.(ai.DecisionProvider)
	e.speech, _ = p.(ai.SpeechProvider)
	e.transcribe, _ = p.(ai.TranscriptionProvider)
	if e == (providers{}) {
		return fmt.Errorf(
			"catalog: provider %q of type %T has no capability: %w",
			id,
			p,
			errors.ErrUnsupported,
		)
	}
	got := c.entryLocked(id)
	got.text = cmp.Or(e.text, got.text)
	got.image = cmp.Or(e.image, got.image)
	got.decision = cmp.Or(e.decision, got.decision)
	got.speech = cmp.Or(e.speech, got.speech)
	got.transcribe = cmp.Or(e.transcribe, got.transcribe)
	addModels(c.models, id, models)
	return nil
}
