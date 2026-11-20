package openai

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"

	"github.com/sonnes/gopi/pkg/ai"
)

// defaultImageFormat is the output format of the API when the response
// names none.
const defaultImageFormat = "png"

// GenerateImage generates images of prompt with the Images API. It reads
// [ai.ImageSize], [ai.AspectRatio], and [ai.ImageCount]. An exact size
// wins over the ratio. The API takes three sizes, so the ratio picks the
// square, the portrait, or the landscape size. The provider ignores
// [ai.ImageResolution]. The request does not set a
// response format, because a GPT Image model always returns base64
// data.
func (p *Provider) GenerateImage(
	ctx context.Context,
	model ai.Model,
	prompt string,
	opts ai.Options,
) (*ai.ImageResponse, error) {
	params := buildImageParams(model, prompt, opts)
	reqOpts := requestOptions(opts)
	send := func() (*sdk.ImagesResponse, error) {
		return p.client.Images.Generate(ctx, params, reqOpts...)
	}
	resp, err := call(ctx, opts, send)
	if err != nil {
		return nil, err
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

// The three sizes of the Images API.
const (
	squareSize    = "1024x1024"
	portraitSize  = "1024x1536"
	landscapeSize = "1536x1024"
)

// sizeOfRatio returns the size of the Images API that is nearest to a
// ratio such as "9:16". It returns "" for a ratio that it cannot read,
// so the API picks the size.
func sizeOfRatio(ratio string) string {
	w, h, ok := strings.Cut(ratio, ":")
	if !ok {
		return ""
	}
	width, errW := strconv.ParseFloat(w, 64)
	height, errH := strconv.ParseFloat(h, 64)
	readable := errW == nil && errH == nil && width > 0 && height > 0
	if !readable {
		return ""
	}
	switch {
	case width > height:
		return landscapeSize
	case width < height:
		return portraitSize
	default:
		return squareSize
	}
}

// buildImageParams builds the request of an image call.
func buildImageParams(model ai.Model, prompt string, opts ai.Options) sdk.ImageGenerateParams {
	params := sdk.ImageGenerateParams{
		Prompt: prompt,
		Model:  sdk.ImageModel(model.ID),
	}

	size, _ := ai.GetOption(opts, ai.ImageSize)
	if size == "" {
		ratio, _ := ai.GetOption(opts, ai.AspectRatio)
		size = sizeOfRatio(ratio)
	}
	if size != "" {
		params.Size = sdk.ImageGenerateParamsSize(size)
	}
	if n, ok := ai.GetOption(opts, ai.ImageCount); ok && n > 0 {
		params.N = param.NewOpt(int64(n))
	}

	return params
}
