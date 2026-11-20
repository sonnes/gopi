package fal

// sizeKind is the set of size fields that an endpoint takes.
type sizeKind int

const (
	// sizeImageSize is the field "image_size". The field holds a preset
	// name, such as "landscape_16_9", or an object with a width and a
	// height.
	sizeImageSize sizeKind = iota
	// sizeAspectRatio is the field "aspect_ratio", such as "9:16".
	sizeAspectRatio
)

// endpoint is a text-to-image endpoint of fal and the fields that its
// input takes. The fields come from the OpenAPI schema of each
// endpoint, at https://api.fal.ai/v1/models?endpoint_id=<id>&expand=openapi-3.0.
type endpoint struct {
	id   string
	name string
	size sizeKind
	// resolutions lists the values of the field "resolution", from the
	// smallest to the largest, in the spelling of the endpoint. If it
	// is empty, the endpoint takes no resolution.
	resolutions []string
	// count reports that the endpoint takes the field "num_images".
	count bool
	// syncMode reports that the endpoint takes the field "sync_mode".
	// Without it, the answer holds a URL and no data.
	syncMode bool
}

// endpoints is the fixed model list. fal has more than a hundred image
// endpoints, so the provider lists a small set of text-to-image models.
// An endpoint that needs an input image is not in the list, because
// [ai.ImageProvider] takes only a prompt.
var endpoints = []endpoint{
	{
		id:          "blackforestlabs/flux-3/text-to-image",
		name:        "FLUX 3",
		size:        sizeAspectRatio,
		resolutions: []string{"1k", "2k", "4k"},
		syncMode:    true,
	},
	{
		id:       "openai/gpt-image-2.6/sunburst/text-to-image",
		name:     "GPT Image 2.6 Sunburst",
		size:     sizeImageSize,
		count:    true,
		syncMode: true,
	},
	{
		id:          "google/nano-banana-2.1",
		name:        "Nano Banana 2.1",
		size:        sizeAspectRatio,
		resolutions: []string{"1K", "2K", "4K"},
		count:       true,
		syncMode:    true,
	},
	{
		id:       "ideogram/v4.5",
		name:     "Ideogram 4.5",
		size:     sizeImageSize,
		count:    true,
		syncMode: true,
	},
	{
		id:   "fal-ai/recraft/v4.1/text-to-image",
		name: "Recraft V4.1",
		size: sizeImageSize,
	},
	{
		// The vector model gives an SVG.
		id:   "fal-ai/recraft/v4.1/text-to-vector",
		name: "Recraft V4.1 Vector",
		size: sizeImageSize,
	},
	{
		id:          "microsoft/mai-image-2.7/text-to-image",
		name:        "MAI-Image-2.7",
		size:        sizeImageSize,
		resolutions: []string{"1K", "2K"},
		syncMode:    true,
	},
	{
		id:       "krea/v3/text-to-image",
		name:     "Krea 3",
		size:     sizeImageSize,
		syncMode: true,
	},
	{
		id:          "bria/fibo-2/generate",
		name:        "Bria Fibo 2",
		size:        sizeAspectRatio,
		resolutions: []string{"1K", "2K"},
		syncMode:    true,
	},
	{
		id:       "fal-ai/flux-2-pro",
		name:     "FLUX 2 Pro",
		size:     sizeImageSize,
		syncMode: true,
	},
	{
		id:       "fal-ai/z-image/turbo",
		name:     "Z-Image Turbo",
		size:     sizeImageSize,
		count:    true,
		syncMode: true,
	},
}

// endpointOf returns the endpoint with the ID id. An ID that is not in
// the list gets the fields of most fal image endpoints: "image_size",
// "num_images", and "sync_mode".
func endpointOf(id string) endpoint {
	for _, e := range endpoints {
		if e.id == id {
			return e
		}
	}
	return endpoint{
		id:       id,
		size:     sizeImageSize,
		count:    true,
		syncMode: true,
	}
}
