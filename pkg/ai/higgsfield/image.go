package higgsfield

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/sonnes/gopi/pkg/ai"
)

// The statuses of a request. Queued and in progress are not final.
const (
	statusQueued     = "queued"
	statusInProgress = "in_progress"
	statusCompleted  = "completed"
)

const (
	// batchSingle and batchFull are the only batch sizes of the SOUL
	// models.
	batchSingle = 1
	batchFull   = 4
	// characterStrength is the strength of a character reference. The
	// API takes 0 to 1, and it cannot process 0 with a character.
	characterStrength = 1.0
	// cancelTimeout caps the cancel call after the context of the
	// generation ends.
	cancelTimeout = 10 * time.Second
	// defaultImageType is the media type of a download that names no
	// image type and has no known extension.
	defaultImageType = "image/png"
)

// aspectRatios are the ratios that the SOUL models take.
var aspectRatios = []string{"9:16", "16:9", "4:3", "3:4", "1:1", "2:3", "3:2"}

// resolutions maps an [ai.ImageResolution] class to a resolution of
// the SOUL models. The highest resolution is 1080p, so 2K and 4K both
// map to it.
var resolutions = map[string]string{
	"1K": "720p",
	"2K": "1080p",
	"4K": "1080p",
}

// imageTypes maps a file extension to an image media type. The table of
// package mime differs between systems, so the provider keeps its own.
var imageTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".webp": "image/webp",
	".gif":  "image/gif",
}

// imageRequest is the body of a submit to a SOUL model.
type imageRequest struct {
	Prompt                  string  `json:"prompt"`
	AspectRatio             string  `json:"aspect_ratio,omitempty"`
	Resolution              string  `json:"resolution,omitempty"`
	BatchSize               int     `json:"batch_size,omitempty"`
	CustomReferenceID       string  `json:"custom_reference_id,omitempty"`
	CustomReferenceStrength float64 `json:"custom_reference_strength,omitempty"`
}

// requestStatus is the body of a submit answer and of a status read.
type requestStatus struct {
	Status    string `json:"status"`
	RequestID string `json:"request_id"`
	StatusURL string `json:"status_url"`
	CancelURL string `json:"cancel_url"`
	Error     string `json:"error"`
	Images    []struct {
		URL string `json:"url"`
	} `json:"images"`
}

// GenerateImage generates images of prompt with the model whose ID is
// the path of its submit endpoint. It reads the status of the request
// every 2 seconds until the status is final, for 5 minutes at most.
// Then it downloads each image.
//
// It maps the options to the values that the SOUL models take:
//
//   - [ai.AspectRatio] goes to the nearest supported ratio. For
//     example, 4:5 goes to 3:4, and 21:9 goes to 16:9.
//   - [ai.ImageResolution] "1K" is 720p. "2K" and "4K" are 1080p.
//   - [ai.ImageCount] 1 is one image. A count of 2 or more is four
//     images, because the models make only batches of 1 or 4.
//   - [ai.Character] is the Soul ID character, at full strength.
//   - [ai.ImageSize] has no effect, because the models take no size.
//
// A submit sends an Idempotency-Key. A retry of the submit sends the
// same key, so the API does not start a second generation.
//
// If ctx ends while the request is queued, it cancels the request, and
// then returns the error of ctx. The API cannot cancel a request after
// it starts.
func (p *Provider) GenerateImage(
	ctx context.Context,
	model ai.Model,
	prompt string,
	opts ai.Options,
) (*ai.ImageResponse, error) {
	req, err := imageBody(prompt, opts)
	if err != nil {
		return nil, fmt.Errorf("higgsfield: images: %w", err)
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("higgsfield: images: %w", err)
	}

	submitted, err := p.submit(ctx, model.ID, body, opts)
	if err != nil {
		return nil, err
	}
	final, err := p.wait(ctx, submitted, opts)
	if err != nil {
		return nil, err
	}
	return p.download(ctx, final)
}

// imageBody maps prompt and opts to the body of a submit.
func imageBody(prompt string, opts ai.Options) (imageRequest, error) {
	req := imageRequest{Prompt: prompt}

	if ratio, ok := ai.GetOption(opts, ai.AspectRatio); ok && ratio != "" {
		nearest, err := nearestRatio(ratio)
		if err != nil {
			return imageRequest{}, err
		}
		req.AspectRatio = nearest
	}

	if class, ok := ai.GetOption(opts, ai.ImageResolution); ok && class != "" {
		resolution, known := resolutions[strings.ToUpper(class)]
		if !known {
			return imageRequest{}, fmt.Errorf("resolution %q: want 1K, 2K, or 4K", class)
		}
		req.Resolution = resolution
	}

	if count, ok := ai.GetOption(opts, ai.ImageCount); ok && count > 0 {
		req.BatchSize = batchFull
		if count == batchSingle {
			req.BatchSize = batchSingle
		}
	}

	if character, ok := ai.GetOption(opts, ai.Character); ok && character != "" {
		req.CustomReferenceID = character
		req.CustomReferenceStrength = characterStrength
	}
	return req, nil
}

// nearestRatio returns the supported ratio that is nearest to ratio, as
// "W:H". It compares the logarithms of the ratios, so a ratio and its
// inverse are at the same distance from 1:1.
func nearestRatio(ratio string) (string, error) {
	want, err := parseRatio(ratio)
	if err != nil {
		return "", err
	}
	nearest := ""
	shortest := math.Inf(1)
	for _, candidate := range aspectRatios {
		value, _ := parseRatio(candidate)
		distance := math.Abs(math.Log(want / value))
		if distance < shortest {
			nearest = candidate
			shortest = distance
		}
	}
	return nearest, nil
}

// parseRatio returns the width divided by the height of "W:H".
func parseRatio(ratio string) (float64, error) {
	width, height, found := strings.Cut(ratio, ":")
	w, errW := strconv.ParseFloat(width, 64)
	h, errH := strconv.ParseFloat(height, 64)
	valid := found && errW == nil && errH == nil && w > 0 && h > 0
	if !valid {
		return 0, fmt.Errorf("aspect ratio %q: want W:H, for example 16:9", ratio)
	}
	return w / h, nil
}

// submit starts a request on the endpoint of modelID.
func (p *Provider) submit(
	ctx context.Context,
	modelID string,
	body []byte,
	opts ai.Options,
) (requestStatus, error) {
	endpoint := p.cfg.baseURL + "/" + strings.TrimPrefix(modelID, "/")
	key := idempotencyKey()
	build := func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		return req, nil
	}
	raw, err := p.call(ctx, opts, build)
	if err != nil {
		return requestStatus{}, err
	}
	var status requestStatus
	if err := json.Unmarshal(raw, &status); err != nil {
		return requestStatus{}, fmt.Errorf("higgsfield: images: submit: %w", err)
	}
	if status.StatusURL == "" {
		return requestStatus{}, fmt.Errorf("higgsfield: images: submit: the answer has no status URL")
	}
	return status, nil
}

// wait reads the status of the submitted request until the status is
// final, and returns the completed status.
func (p *Provider) wait(
	ctx context.Context,
	submitted requestStatus,
	opts ai.Options,
) (requestStatus, error) {
	deadline := time.Now().Add(p.cfg.pollTimeout)
	last := submitted.Status
	for {
		status, err := p.status(ctx, submitted.StatusURL, opts)
		if err != nil && ctx.Err() != nil {
			return requestStatus{}, p.abandon(ctx, submitted, last, ctx.Err())
		}
		if err != nil {
			return requestStatus{}, err
		}
		last = status.Status

		switch status.Status {
		case statusCompleted:
			return status, nil
		case statusQueued, statusInProgress:
		default:
			return requestStatus{}, finalError(submitted.RequestID, status)
		}

		if time.Now().After(deadline) {
			err := fmt.Errorf(
				"higgsfield: images: request %s is still %s after %s",
				submitted.RequestID,
				last,
				p.cfg.pollTimeout,
			)
			return requestStatus{}, p.abandon(ctx, submitted, last, err)
		}

		timer := time.NewTimer(p.cfg.pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return requestStatus{}, p.abandon(ctx, submitted, last, ctx.Err())
		case <-timer.C:
		}
	}
}

// status reads the status of a request.
func (p *Provider) status(ctx context.Context, statusURL string, opts ai.Options) (requestStatus, error) {
	build := func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, statusURL, nil)
	}
	raw, err := p.call(ctx, opts, build)
	if err != nil {
		return requestStatus{}, err
	}
	var status requestStatus
	if err := json.Unmarshal(raw, &status); err != nil {
		return requestStatus{}, fmt.Errorf("higgsfield: images: status: %w", err)
	}
	return status, nil
}

// abandon cancels the request if its last status is queued, and returns
// err. The cancel is best effort, so its own error is dropped. It uses a
// new context, because ctx can be over.
func (p *Provider) abandon(ctx context.Context, submitted requestStatus, last string, err error) error {
	if last != statusQueued || submitted.CancelURL == "" {
		return err
	}
	cancelCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cancelTimeout)
	defer cancel()
	req, reqErr := http.NewRequestWithContext(cancelCtx, http.MethodPost, submitted.CancelURL, nil)
	if reqErr != nil {
		return err
	}
	p.authorize(req, nil)
	_, _ = p.do(req)
	return err
}

// finalError returns the error of a request that ended with no images.
func finalError(requestID string, status requestStatus) error {
	if status.Error != "" {
		return fmt.Errorf("higgsfield: images: request %s is %s: %s", requestID, status.Status, status.Error)
	}
	return fmt.Errorf("higgsfield: images: request %s is %s", requestID, status.Status)
}

// download reads each image of a completed request. It sends no key,
// because the files are on another host.
func (p *Provider) download(ctx context.Context, completed requestStatus) (*ai.ImageResponse, error) {
	images := make([]ai.GeneratedImage, 0, len(completed.Images))
	for _, img := range completed.Images {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, img.URL, nil)
		if err != nil {
			return nil, fmt.Errorf("higgsfield: images: download: %w", err)
		}
		resp, err := p.cfg.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("higgsfield: images: download: %w", err)
		}
		data, err := readImage(resp)
		if err != nil {
			return nil, fmt.Errorf("higgsfield: images: download: %w", err)
		}
		images = append(images, ai.GeneratedImage{
			Data:      data,
			MediaType: imageType(resp.Header.Get("Content-Type"), img.URL),
		})
	}
	return &ai.ImageResponse{Images: images}, nil
}

// readImage reads the body of a download and closes it.
func readImage(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		body := buf.String()
		if len(body) > maxErrorBody {
			body = body[:maxErrorBody]
		}
		return nil, &Error{
			StatusCode: resp.StatusCode,
			Header:     resp.Header,
			Body:       body,
		}
	}
	return buf.Bytes(), nil
}

// imageType returns the media type of a download: the Content-Type if
// it is an image type, else the type of the extension of fileURL, else
// [defaultImageType].
func imageType(contentType, fileURL string) string {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err == nil && strings.HasPrefix(mediaType, "image/") {
		return mediaType
	}
	if u, err := url.Parse(fileURL); err == nil {
		ext := strings.ToLower(path.Ext(u.Path))
		if t, ok := imageTypes[ext]; ok {
			return t
		}
	}
	return defaultImageType
}

// idempotencyKey returns a random key of 26 base32 characters. The
// API takes 1 to 255 visible ASCII characters.
func idempotencyKey() string {
	return rand.Text()
}
