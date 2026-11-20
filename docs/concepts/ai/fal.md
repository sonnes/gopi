---
title: fal Provider
description: How the fal provider generates images with a sync call to a fixed list of text-to-image endpoints, and maps the size options to the fields of each endpoint.
package: pkg/ai/fal
read_when:
  - You change a request that the fal provider sends
  - You add an endpoint to the model list of fal
  - An image of fal has the wrong size or shape
---

# fal Provider

The fal provider serves the image models of fal.ai with plain HTTP requests. It has image generation, and no text generation. A model ID is the endpoint ID of fal, such as `fal-ai/flux-2-pro`, so a spec is `fal/<endpoint ID>`.

```
ai.ImageModel -> POST fal.run/{endpoint ID}    JSON in, data URIs out
CheckKey ------> GET  api.fal.ai/v1/models/pricing
ListModels ----> the fixed list in models.go, no call
                   header Authorization: Key <key>
```

## Design

- The provider uses `net/http` and no SDK. gopi calls one route for images and one route for the key check, so a client of its own is small.
- An image call is a sync call with `sync_mode` set. The answer holds each image as a data URI, so the image has data and no URL that expires. An endpoint without `sync_mode` gives a URL, and the provider downloads it, because a caller needs the bytes. A vector endpoint, such as `fal-ai/recraft/v4.1/text-to-vector`, gives an SVG with the media type `image/svg+xml`.
- The model list is a fixed list of text-to-image endpoints. fal has more than a hundred image endpoints, and each one has its own input fields. An endpoint that needs an input image is not in the list, because an image call takes only a prompt.
- Each entry of the list records its size fields: `image_size` or `aspect_ratio`. The provider maps the size options to these fields and drops an option that the endpoint does not take. An endpoint ID that is not in the list gets `image_size`, `num_images`, and `sync_mode`, the fields of most fal endpoints.
- For `image_size`, an explicit size goes as a width and a height. Else the aspect ratio goes to the nearest preset. The resolution goes only to an endpoint that also takes `resolution`.
- For `aspect_ratio`, the aspect ratio goes as it is. The resolution goes in the spelling of the endpoint, at the nearest class that the endpoint takes.
- A new account runs two requests at the same time. A third sync call gets status 429, and `internal/retry` retries it only when the call sets `ai.MaxRetries`.
- A key check reads the prices of the first endpoint of the list. The route needs a key and runs no model. The API gives no facts about a key, so the info is zero.
- A failed answer is an `*Error` with the status, the header, and the start of the body.

## Map

| Task                                        | File                     |
| ------------------------------------------- | ------------------------ |
| Change the key, the base URLs, or the client | `pkg/ai/fal/fal.go`      |
| Change the retries or read an error         | `pkg/ai/fal/request.go`  |
| Add an endpoint or change its fields        | `pkg/ai/fal/models.go`   |
| Map the options of an image call            | `pkg/ai/fal/image.go`    |
| List the models or check a key              | `pkg/ai/fal/list.go`     |

## Related Concepts

- [Provider](provider.md)
- [Catalog](../catalog/catalog.md)
