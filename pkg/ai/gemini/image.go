package gemini

import (
	"context"
	"fmt"
	"net/http"

	"google.golang.org/genai"

	"github.com/sonnes/gopi/pkg/ai"
)

var _ ai.ImageProvider = (*Provider)(nil)

// GenerateImage generates images with an image model on generateContent,
// such as gemini-nano-banana-2.1. Imagen is shut down, so the image
// models are the only image API. One request gives one image, so for
// [ai.ImageCount] images the provider sends that number of requests.
// The provider sends [ai.ImageSize] as it is. If the model does not take
// the size, the API returns a [*Error].
func (p *Provider) GenerateImage(
	ctx context.Context,
	model ai.Model,
	prompt string,
	opts ai.Options,
) (*ai.ImageResponse, error) {
	if p.err != nil {
		return nil, p.err
	}

	config := &genai.GenerateContentConfig{
		ResponseModalities: []string{string(genai.ModalityImage)},
	}
	if size, ok := ai.GetOption(opts, ai.ImageSize); ok {
		config.ImageConfig = &genai.ImageConfig{ImageSize: size}
	}
	if headers, ok := ai.GetOption(opts, ai.Headers); ok {
		h := make(http.Header, len(headers))
		for k, v := range headers {
			h.Set(k, v)
		}
		config.HTTPOptions = &genai.HTTPOptions{Headers: h}
	}

	count := 1
	if n, ok := ai.GetOption(opts, ai.ImageCount); ok && n > 1 {
		count = n
	}

	var images []ai.GeneratedImage
	for range count {
		send := func() (*genai.GenerateContentResponse, error) {
			resp, err := p.client.Models.GenerateContent(
				ctx,
				model.ID,
				genai.Text(prompt),
				config,
			)
			return resp, apiError(err)
		}
		resp, err := call(ctx, opts, send)
		if err != nil {
			return nil, err
		}
		got, err := responseImages(resp)
		if err != nil {
			return nil, err
		}
		images = append(images, got...)
	}
	return &ai.ImageResponse{Images: images}, nil
}

// A response with no image is an error that names the finish reason,
// such as IMAGE_SAFETY.
func responseImages(resp *genai.GenerateContentResponse) ([]ai.GeneratedImage, error) {
	var images []ai.GeneratedImage
	var reason genai.FinishReason
	for _, c := range resp.Candidates {
		reason = c.FinishReason
		if c.Content == nil {
			continue
		}
		for _, part := range c.Content.Parts {
			if part.InlineData == nil {
				continue
			}
			images = append(images, ai.GeneratedImage{
				Data:      part.InlineData.Data,
				MediaType: part.InlineData.MIMEType,
			})
		}
	}
	if len(images) == 0 {
		return nil, fmt.Errorf("gemini: no image in the response, finish reason %q", reason)
	}
	return images, nil
}
