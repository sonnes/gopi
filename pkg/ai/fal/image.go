package fal

import (
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/sonnes/gopi/pkg/ai"
)

// imageSize is the field "image_size" as a width and a height.
type imageSize struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// imageRequest is the body of a sync call of a text-to-image endpoint.
// The image size is a preset name or an [imageSize].
type imageRequest struct {
	Prompt      string `json:"prompt"`
	SyncMode    bool   `json:"sync_mode,omitempty"`
	ImageSize   any    `json:"image_size,omitempty"`
	AspectRatio string `json:"aspect_ratio,omitempty"`
	Resolution  string `json:"resolution,omitempty"`
	NumImages   int    `json:"num_images,omitempty"`
}

// imageResponse is the body of an answer of a sync call.
type imageResponse struct {
	Images []struct {
		URL         string `json:"url"`
		ContentType string `json:"content_type"`
	} `json:"images"`
}

// GenerateImage generates images of prompt with a sync call to the
// endpoint of model. The call sets sync_mode, so the answer holds each
// image as a data URI. An endpoint without sync_mode gives an image
// URL, and the provider downloads it, because a caller needs the bytes.
// The download sends no key, because the file is on the file host of
// fal.
//
// The size options go to the fields that the endpoint takes:
//
//   - An endpoint with "image_size" gets [ai.ImageSize] as a width and a
//     height. Else it gets the preset nearest to [ai.AspectRatio]. It
//     gets [ai.ImageResolution] only if it also takes "resolution".
//   - An endpoint with "aspect_ratio" gets [ai.AspectRatio] as it is,
//     and [ai.ImageResolution] in its own spelling. It ignores
//     [ai.ImageSize].
//
// [ai.ImageCount] goes to "num_images" if the endpoint takes it. fal
// has no trained characters, so a call with [ai.Character] returns an
// error that wraps [errors.ErrUnsupported].
func (p *Provider) GenerateImage(
	ctx context.Context,
	model ai.Model,
	prompt string,
	opts ai.Options,
) (*ai.ImageResponse, error) {
	if character, ok := ai.GetOption(opts, ai.Character); ok && character != "" {
		return nil, fmt.Errorf("fal: images: a character: %w", errors.ErrUnsupported)
	}
	req, err := newImageRequest(endpointOf(model.ID), prompt, opts)
	if err != nil {
		return nil, fmt.Errorf("fal: images: %w", err)
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("fal: images: %w", err)
	}
	// The endpoint ID is a path of slashes, so it goes into the URL as
	// it is.
	target := p.cfg.baseURL + "/" + model.ID

	build := func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	}
	raw, err := p.call(ctx, opts, build)
	if err != nil {
		return nil, err
	}

	var resp imageResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("fal: images: %w", err)
	}
	got, err := resp.images()
	if err != nil {
		return nil, err
	}
	for i, img := range got.Images {
		if img.URL == "" {
			continue
		}
		data, mediaType, err := p.download(ctx, img.URL)
		if err != nil {
			return nil, fmt.Errorf("fal: images: download: %w", err)
		}
		got.Images[i] = ai.GeneratedImage{
			Data:      data,
			MediaType: cmp.Or(img.MediaType, mediaType),
		}
	}
	return got, nil
}

// download returns the bytes and the media type of the file at url.
func (p *Provider) download(ctx context.Context, url string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := p.cfg.httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	return data, resp.Header.Get("Content-Type"), nil
}

// newImageRequest maps the options to the fields that e takes.
func newImageRequest(e endpoint, prompt string, opts ai.Options) (imageRequest, error) {
	req := imageRequest{
		Prompt:   prompt,
		SyncMode: e.syncMode,
	}
	if e.count {
		req.NumImages, _ = ai.GetOption(opts, ai.ImageCount)
	}
	if resolution, ok := ai.GetOption(opts, ai.ImageResolution); ok && len(e.resolutions) > 0 {
		req.Resolution = nearestResolution(resolution, e.resolutions)
	}
	ratio, _ := ai.GetOption(opts, ai.AspectRatio)

	if e.size == sizeAspectRatio {
		req.AspectRatio = ratio
		return req, nil
	}
	if size, ok := ai.GetOption(opts, ai.ImageSize); ok && size != "" {
		parsed, err := parseSize(size)
		if err != nil {
			return imageRequest{}, err
		}
		req.ImageSize = parsed
		return req, nil
	}
	if preset := nearestPreset(ratio); preset != "" {
		req.ImageSize = preset
	}
	return req, nil
}

// parseSize reads a size such as "1024x768".
func parseSize(size string) (imageSize, error) {
	w, h, ok := strings.Cut(size, "x")
	width, errW := strconv.Atoi(w)
	height, errH := strconv.Atoi(h)
	valid := ok && errW == nil && errH == nil && width > 0 && height > 0
	if !valid {
		return imageSize{}, fmt.Errorf("the size %q is not WIDTHxHEIGHT", size)
	}
	return imageSize{Width: width, Height: height}, nil
}

// preset is a preset name of the field "image_size" and its ratio of
// the width to the height.
type preset struct {
	name  string
	ratio float64
}

// presets are the preset names that each endpoint with "image_size"
// takes. "square_hd" is the larger of the two square presets.
var presets = []preset{
	{name: "square_hd", ratio: 1},
	{name: "portrait_4_3", ratio: 3.0 / 4},
	{name: "portrait_16_9", ratio: 9.0 / 16},
	{name: "landscape_4_3", ratio: 4.0 / 3},
	{name: "landscape_16_9", ratio: 16.0 / 9},
}

// nearestPreset returns the preset nearest to ratio, such as "9:16". It
// compares the logarithms of the ratios, so 2:3 and 3:2 are at the same
// distance from 1:1. It returns "" if ratio is not WIDTH:HEIGHT, such
// as "auto".
func nearestPreset(ratio string) string {
	w, h, ok := strings.Cut(ratio, ":")
	width, errW := strconv.ParseFloat(w, 64)
	height, errH := strconv.ParseFloat(h, 64)
	valid := ok && errW == nil && errH == nil && width > 0 && height > 0
	if !valid {
		return ""
	}
	want := math.Log(width / height)
	best := presets[0]
	for _, p := range presets[1:] {
		closer := math.Abs(math.Log(p.ratio)-want) < math.Abs(math.Log(best.ratio)-want)
		if closer {
			best = p
		}
	}
	return best.name
}

// resolutionClasses are the size classes of [ai.ImageResolution], by
// the number of thousands of pixels on the long side.
var resolutionClasses = map[string]int{
	"1K": 1,
	"2K": 2,
	"4K": 4,
}

// nearestResolution returns the value of have nearest to the class
// want, in the spelling of have. If two values are at the same
// distance, it returns the smaller. If want is not a known class, it
// returns want as it is, and the API checks it.
func nearestResolution(want string, have []string) string {
	wantClass, ok := resolutionClasses[strings.ToUpper(want)]
	if !ok {
		return want
	}
	best := ""
	bestDistance := math.MaxInt
	for _, h := range have {
		class, ok := resolutionClasses[strings.ToUpper(h)]
		if !ok {
			continue
		}
		distance := max(class-wantClass, wantClass-class)
		if distance < bestDistance {
			best = h
			bestDistance = distance
		}
	}
	return best
}

// images returns the images of r. A data URI becomes the data of the
// image. Another URL stays a URL.
func (r imageResponse) images() (*ai.ImageResponse, error) {
	images := make([]ai.GeneratedImage, 0, len(r.Images))
	for _, img := range r.Images {
		if !strings.HasPrefix(img.URL, "data:") {
			images = append(images, ai.GeneratedImage{
				URL:       img.URL,
				MediaType: img.ContentType,
			})
			continue
		}
		data, mediaType, err := decodeDataURI(img.URL)
		if err != nil {
			return nil, fmt.Errorf("fal: images: decode: %w", err)
		}
		if mediaType == "" {
			mediaType = img.ContentType
		}
		images = append(images, ai.GeneratedImage{
			Data:      data,
			MediaType: mediaType,
		})
	}
	return &ai.ImageResponse{Images: images}, nil
}

// decodeDataURI returns the data and the media type of a data URI, such
// as "data:image/png;base64,iVBOR...".
func decodeDataURI(uri string) ([]byte, string, error) {
	meta, payload, ok := strings.Cut(strings.TrimPrefix(uri, "data:"), ",")
	if !ok {
		return nil, "", errors.New("a data URI with no comma")
	}
	mediaType, _, _ := strings.Cut(meta, ";")
	if strings.HasSuffix(meta, ";base64") {
		data, err := base64.StdEncoding.DecodeString(payload)
		return data, mediaType, err
	}
	text, err := url.PathUnescape(payload)
	return []byte(text), mediaType, err
}
