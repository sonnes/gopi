package openai

import (
	"context"
	"encoding/base64"
	"fmt"

	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"

	"github.com/sonnes/gopi/pkg/ai"
)

// defaultImageFormat is the output format of the API when the response
// names none.
const defaultImageFormat = "png"

// GenerateImage generates images of prompt with the Images API. It reads
// [ai.ImageSize] and [ai.ImageCount]. The request does not set a
// response format, because a GPT Image model always returns base64
// data.
func (p *Provider) GenerateImage(
	ctx context.Context,
	model ai.Model,
	prompt string,
	opts ai.Options,
) (*ai.ImageResponse, error) {
	params := buildImageParams(model, prompt, opts)

	resp, err := p.client.Images.Generate(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("openai: %w", err)
	}

	format := string(resp.OutputFormat)
	if format == "" {
		format = defaultImageFormat
	}
	mediaType := "image/" + format

	images := make([]ai.GeneratedImage, 0, len(resp.Data))
	for _, img := range resp.Data {
		if img.B64JSON == "" {
			images = append(images, ai.GeneratedImage{URL: img.URL})
			continue
		}

		data, err := base64.StdEncoding.DecodeString(img.B64JSON)
		if err != nil {
			return nil, fmt.Errorf("openai: decode image: %w", err)
		}
		images = append(images, ai.GeneratedImage{
			Data:      data,
			MediaType: mediaType,
		})
	}

	return &ai.ImageResponse{Images: images}, nil
}

// buildImageParams builds the request of an image call.
func buildImageParams(model ai.Model, prompt string, opts ai.Options) sdk.ImageGenerateParams {
	params := sdk.ImageGenerateParams{
		Prompt: prompt,
		Model:  sdk.ImageModel(model.ID),
	}

	if size, ok := ai.GetOption(opts, ai.ImageSize); ok && size != "" {
		params.Size = sdk.ImageGenerateParamsSize(size)
	}
	if n, ok := ai.GetOption(opts, ai.ImageCount); ok && n > 0 {
		params.N = param.NewOpt(int64(n))
	}

	return params
}
