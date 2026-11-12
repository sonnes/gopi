---
title: Gemini Provider
description: How the Gemini provider sends a stateless history to the Interactions API, keeps thought signatures between calls, and calls the models API for images and the model list.
package: pkg/ai/gemini
read_when:
  - You change a request that the Gemini provider sends
  - A Gemini model loses its reasoning between tool calls
  - A Gemini call retries when it must not
  - You record a Gemini provider test
---

# Gemini Provider

The Gemini provider serves the Gemini Developer API with the Go SDK of Google. Text and objects go to the Interactions API. Images and the model list go to the models API of the same SDK.

```
                   +--> Interactions API   text, objects
ai.LanguageModel --+      store: false, the whole history
                   |
ai.ImageModel -----+--> models API         images, model list
                          generateContent, models.list
```

## Design

- The provider sets `store` to false and sends the whole history in each request. gopi keeps the history in its messages, so the API keeps nothing.
- Each `thought` step becomes a thinking block, with its signature as JSON in `Signature`. The next request sends the step again in its place. A signature that does not decode comes from another provider, and the provider drops that block.
- Gemini 3 and Gemini 4 refuse a function call turn with no signature. If a tool call came from another model, the provider sends a thought step with the skip signature of the API before it. A session can then change models between tool calls.
- The input has no system role. A system message goes as a system-reminder in a user turn, and the request sends the current tools, because no step adds or removes a tool.
- Each request turns the retries of the SDK off, so `internal/retry` decides each retry, before the first event only.
- The SDK resumes a turn with the status `requires_continuation` by itself. Each hop bills the input again, and the usage holds the sum.
- The provider does not send `ai.Temperature`, because the SDK marks the temperature of the Interactions API as deprecated.
- The provider has no token count, because the Interactions API has none.
- `GenerateImage` calls an image model on `generateContent`. One request gives one image.

## Map

| Task                                 | File                         |
| ------------------------------------ | ---------------------------- |
| Build an Interactions request        | `pkg/ai/gemini/request.go`   |
| Convert messages to steps            | `pkg/ai/gemini/convert.go`   |
| Read an Interactions stream          | `pkg/ai/gemini/stream.go`    |
| Change the retries or read an error  | `pkg/ai/gemini/retry.go`     |
| Generate an object                   | `pkg/ai/gemini/object.go`    |
| Generate an image                    | `pkg/ai/gemini/image.go`     |
| List the models or check a key       | `pkg/ai/gemini/list.go`      |
| Record a provider test               | `pkg/ai/gemini/cases_test.go` |

## Related Concepts

- [Provider](provider.md)
- [Message](message.md)
- [Stream](stream.md)
