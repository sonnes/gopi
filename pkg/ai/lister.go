package ai

import "context"

// ModelLister lists the models that a provider serves to its
// credentials, or that an agent CLI serves to its login. A provider
// implements it together with [TextProvider] or [ImageProvider], and a
// driver of a CLI agent implements it too.
//
// The list is on the provider or the driver and not on a binding,
// because it does not belong to one model. A field that the API does not return stays zero,
// so a caller can merge the list with the models that it already knows.
type ModelLister interface {
	ListModels(ctx context.Context) ([]Model, error)
}
