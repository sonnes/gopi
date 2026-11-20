package openrouter

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/sonnes/gopi/internal/httpapi"
	"github.com/sonnes/gopi/internal/retry"
	"github.com/sonnes/gopi/pkg/ai"
)

var _ ai.ImageProvider = (*Provider)(nil)

// imagesRoute is the route of the Images API, under the base URL.
const imagesRoute = "/images"

// defaultImageType is the media type of an image whose response names
// none. The Images API returns a PNG by default.
const defaultImageType = "image/png"

// imageRequest is the body of POST /images. The API returns base64 data
// when the request sets no response_format, so the provider sets none.
type imageRequest struct {
	Model       string `json:"model"`
	Prompt      string `json:"prompt"`
	AspectRatio string `json:"aspect_ratio,omitempty"`
	Resolution  string `json:"resolution,omitempty"`
	Size        string `json:"size,omitempty"`
	N           int    `json:"n,omitempty"`
}

// imageResponse is the body of an answer of POST /images.
type imageResponse struct {
	Data []struct {
		B64JSON   string `json:"b64_json"`
		MediaType string `json:"media_type"`
		URL       string `json:"url"`
	} `json:"data"`
}

// GenerateImage generates images of prompt with the Images API of
// OpenRouter. It sends [ai.AspectRatio], [ai.ImageResolution],
// [ai.ImageSize], and [ai.ImageCount] as they are. If the model does not
// take a value, the API answers with status 400, and the message of the
// error lists the values that the model takes. OpenRouter has no trained
// characters, so a call with [ai.Character] returns an error that wraps
// [errors.ErrUnsupported]. It retries as the other calls of the
// provider do.
func (p *Provider) GenerateImage(
	ctx context.Context,
	model ai.Model,
	prompt string,
	opts ai.Options,
) (*ai.ImageResponse, error) {
	if character, ok := ai.GetOption(opts, ai.Character); ok && character != "" {
		return nil, fmt.Errorf("openrouter: images: a character: %w", errors.ErrUnsupported)
	}

	req := imageRequest{
		Model:  model.ID,
		Prompt: prompt,
	}
	req.AspectRatio, _ = ai.GetOption(opts, ai.AspectRatio)
	req.Resolution, _ = ai.GetOption(opts, ai.ImageResolution)
	req.Size, _ = ai.GetOption(opts, ai.ImageSize)
	req.N, _ = ai.GetOption(opts, ai.ImageCount)
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("openrouter: images: %w", err)
	}

	header := http.Header{}
	headers, _ := ai.GetOption(opts, ai.Headers)
	for name, value := range headers {
		header.Set(name, value)
	}
	url := p.cfg.baseURL + imagesRoute
	send := func() ([]byte, error) {
		return httpapi.Post(ctx, p.cfg.httpClient, url, p.cfg.apiKey, body, header)
	}
	raw, err := retry.Call(ctx, opts, "openrouter: images", statusOf, send)
	if err != nil {
		return nil, err
	}

	var resp imageResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("openrouter: images: %w", err)
	}
	return resp.images()
}

// images returns the images of r. An image with a URL and no data keeps
// the URL.
func (r imageResponse) images() (*ai.ImageResponse, error) {
	images := make([]ai.GeneratedImage, 0, len(r.Data))
	for _, img := range r.Data {
		if img.B64JSON == "" {
			images = append(images, ai.GeneratedImage{URL: img.URL})
			continue
		}
		data, err := base64.StdEncoding.DecodeString(img.B64JSON)
		if err != nil {
			return nil, fmt.Errorf("openrouter: images: decode: %w", err)
		}
		mediaType := img.MediaType
		if mediaType == "" {
			mediaType = defaultImageType
		}
		images = append(images, ai.GeneratedImage{
			Data:      data,
			MediaType: mediaType,
		})
	}
	return &ai.ImageResponse{Images: images}, nil
}
