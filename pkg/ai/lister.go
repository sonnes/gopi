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

// KeyChecker checks the credentials of a provider with one call that
// runs no model, such as a call to list the models. A caller uses it to
// tell a person if a key works before the first message.
//
// A refused key is an error that wraps the error of the API. The info
// is zero for an API that gives only a yes or a no.
type KeyChecker interface {
	CheckKey(ctx context.Context) (KeyInfo, error)
}

// KeyInfo is what an API says about a key that it accepts.
type KeyInfo struct {
	// Label is the name of the key, if the API gives one.
	Label string
	// Usage is the credit that the key spent, in US dollars.
	Usage float64
	// Limit is the most credit that the key can spend, in US dollars.
	// It is set only when Limited is true.
	Limit float64
	// Limited is true when the key has a credit limit.
	Limited bool
	// FreeTier is true when the key is on the free tier of the API.
	FreeTier bool
}
