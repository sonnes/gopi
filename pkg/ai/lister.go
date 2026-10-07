package ai

import "context"

// ModelLister lists the models that a provider serves to its
// credentials. A provider implements it together with [TextProvider] or
// [ImageProvider].
//
// The list is on the provider and not on a binding, because it does not
// belong to one model. A field that the API does not return stays zero,
// so a caller can merge the list with the models that it already knows.
type ModelLister interface {
	ListModels(ctx context.Context) ([]Model, error)
}
