package ai

import "context"

// ImageProvider generates images from a text prompt.
type ImageProvider interface {
	GenerateImage(
		ctx context.Context,
		model Model,
		prompt string,
		opts Options,
	) (*ImageResponse, error)
}

// ImageResponse holds the generated images.
type ImageResponse struct {
	Images []GeneratedImage
}

// GeneratedImage is one image. A provider sets Data or URL.
type GeneratedImage struct {
	Data      []byte
	MediaType string
	URL       string
}

// ImageModel is a [Model] bound to an [ImageProvider].
type ImageModel struct {
	Model    Model
	Provider ImageProvider
}

// GenerateImage generates images of prompt from the bound provider.
func (im ImageModel) GenerateImage(
	ctx context.Context,
	prompt string,
	opts ...Options,
) (*ImageResponse, error) {
	return im.Provider.GenerateImage(ctx, im.Model, prompt, JoinOptions(opts...))
}
