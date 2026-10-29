---
title: Provider
description: How a provider serves a model call, and how the Anthropic provider builds its requests.
package: pkg/ai, pkg/ai/anthropic, internal/retry, internal/transcript, internal/providertest
read_when:
  - You bind a model to a provider
  - You add an option that one provider reads
  - You write a provider
  - You change a request that the Anthropic provider sends
  - You record a provider test
---

# Provider

A provider is one backend that answers a model call. It holds the credentials. A model is metadata and holds none. A binding pairs the two, and the layers above call the binding.

```
LanguageModel{Model, Provider}
   |
   +-- StreamText     -> TextProvider    (required)
   +-- GenerateObject -> ObjectProvider  (optional)
   +-- CountTokens    -> TokenCounter    (optional)

ImageModel{Model, Provider}  -> ImageProvider
SpeechModel{Model, Provider} -> SpeechProvider
```

## Design

- An option of `ai` is a setting that each provider API has, such as retries, temperature, or cache retention. A setting that one provider reads is an option of that provider package, and it travels in the same list.
- Each provider retries a request only before the first event of the stream, because a retry after an event repeats output. An error after the first event, such as a dropped connection, ends the stream with no retry.
- The SDK of a provider does not retry, so `internal/retry` can cap the wait that a server asks for. A provider retries only when the call sets `ai.MaxRetries`, so the layer above decides if a call retries.
- `internal/transcript` holds the rules that every provider applies to the messages, and `internal/providertest` holds the cases that every provider must pass. Each provider module uses the same copy, so the providers do not drift apart.
- A provider reads the limits and the prices of a model from the `ai.Model` of the call, such as a model of the [model file](../catalog/model-data.md). A zero field is unknown, so a model with only its ID works with the defaults of the provider.
- A provider does not check a thinking level against the model, and the API rejects a level that the model does not take.
- Each provider is its own module under `pkg/ai`, so a program that uses one provider does not download the SDK of another.
- Anthropic object generation uses structured outputs, because Claude Fable 5.1, Opus 5.5, and Sonnet 5.5 reject a forced tool call.
- A model that manages its effort gets the same effort at the top of each request. An effort message goes before each assistant message and at the end, so a change of level changes only the end of the request.
- A model that manages its effort always thinks adaptively, so off is not one of its levels. The API drops a thinking block whose prefix does not match, so a change of effort causes no error.

## Map

| Task                                   | File                                    |
| -------------------------------------- | --------------------------------------- |
| Bind a model to a provider             | `pkg/ai/text.go`                        |
| Add an option                          | `pkg/ai/options.go`                     |
| Add an optional capability             | `pkg/ai/object.go`, `pkg/ai/tokens.go`  |
| Mark what a Claude model accepts       | `pkg/ai/anthropic/compat.go`            |
| Change the retry loop                  | `internal/retry/retry.go`               |
| Change a rule of every provider        | `internal/transcript/transcript.go`     |
| Add a case that every provider passes  | `internal/providertest/providertest.go` |
| Build an Anthropic request             | `pkg/ai/anthropic/params.go`            |
| Convert messages for Anthropic         | `pkg/ai/anthropic/convert.go`           |
| Record a provider test                 | `pkg/ai/anthropic/anthropic_test.go`    |
| Generate an image or speech            | `pkg/ai/image.go`, `pkg/ai/speech.go`   |

## Related Concepts

- [Message](message.md)
- [Stream](stream.md)
- [OpenAI Providers](openai.md)
