---
title: Provider
description: How a provider serves a model call, and how the Anthropic provider builds its requests.
package: pkg/ai, pkg/ai/anthropic
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

## Decisions

- A binding is a struct, not an interface, so middleware wraps the provider. GenerateObject and CountTokens assert the optional capability on the provider field. An interface per capability makes every wrapper forward each one.
- The messages are the only argument of a call. The system prompt and the first tools are options. A system message in the messages adds to the prompt from its place. [Message](message.md) tells which models take one. A prompt struct that held the messages, the prompt, and the tools lost, because every input beyond the messages then takes the same path, and a call reads flat.
- An option is a typed value with its own key, in the style of encoding/json/v2. A provider package defines its own options, and they travel in the same list. With one struct of fields, a setting for one provider becomes a field that every other provider ignores.
- Unset is a state of its own. A provider reads an option and gets a flag that says whether the caller set it. A pointer field needs a nil check.
- A setter is named for what it sets, with no With prefix. When the name collides with a type, the type takes a suffix.
- Retries, temperature, tool choice, headers, and cache retention are options of `ai`, because each provider API has them. A provider drops an option that a model rejects, such as temperature on the Claude 5 models, which return a 400 for it. There is no session ID, because only OpenRouter reads one.
- The tool choice is unset by default, so the model decides on each call. An agent sets it for one call only, because a forced call on every turn never lets the loop end. Claude Fable 5.1 rejects a forced tool, and the provider does not check.
- The provider retries a request itself, and only before the first event of the stream. The retry loop of the SDK lost, because it obeys any wait that the server asks for, and a long wait looks like a hung call. No retry is the default, so the layer above sets the policy.
- An empty or unknown stop reason is an error. A stream that ends with no stop reason ended early, and a new reason of the API must not pass as a normal stop.
- A thinking level maps to an effort of the API. A model lists the levels that it takes, but the provider does not check the list. A pass-through model has no list, and the API rejects a level with a clear error. No rank resolves a level to the closest one that a model supports.
- A model holds its limits, its input kinds, its thinking levels, and its prices. Each provider package fills them from the docs of the API, so a caller can pick a model or show a price without a second table. A zero field is unknown, so a pass-through model works with only its ID.
- Each provider is its own module under `pkg/ai`, so a program that uses one provider does not download the SDK of another, and the root module has no SDK. `go.work` joins the modules in this repository. A sub-package of the root module lost, because every user of `ai` then got every SDK.
- A provider sends the `ai.SystemPrompt` option as the system prompt of its API. The option takes a slice of blocks, so a provider reads it with `GetOption` like every other option. A variadic setter lost, because it cannot be the key of `GetOption` and needed a reader of its own.
- The Anthropic provider puts a cache breakpoint on the last system block, the last tool, and the last block of the last user or system message. The system prompt, the tools, and the history then each have a breakpoint, and each marker carries the lifetime that `ai.CacheRetention` sets. The top-level automatic marker lost, because pi-mono measured its cache hits with these breakpoints.
- With tool changes, the Anthropic tool list holds the first tools and a deferred placeholder tool, and it never changes. The API adds a hidden scaffold for tool changes, and the placeholder puts it in the cached prefix from the first request. pi-mono measured a full cache miss on the first tool change without it.
- Every Anthropic tool streams its input eagerly, so a tool delta arrives as the model writes it. An input that stops at the output cap can be invalid JSON, and the tool reports it as an error result.
- Anthropic object generation uses structured outputs, not a forced tool call. Claude Fable 5.1, Opus 5.5, and Sonnet 5.5 reject a forced tool call.
- The Anthropic provider maps every thinking level except off to adaptive thinking with an effort. Haiku 4.5 thinks only with a token budget, so its model lists only off.
- A model that manages its effort gets the same effort at the top of each request, and an effort-only system message before each assistant message and at the end. A change of level then changes only the end of the request, and the cached prefix stays. Its thinking is always adaptive, with the control that drops a thinking block whose prefix does not match, so off is not a level of these models.
- A provider test records the traffic and replays it. The suite skips a test with no recording, so it runs without a key.
- A token count sends the same messages, system prompt, tools, and thinking config as a call, so the count matches.
- The provider has no OAuth, no environment detection, and no logging. Each belongs to a later layer.

## Map

| Task                                   | File                                   |
| -------------------------------------- | -------------------------------------- |
| Bind a model to a provider             | `pkg/ai/text.go`                       |
| Add an option                          | `pkg/ai/options.go`                    |
| Add an optional capability             | `pkg/ai/object.go`, `pkg/ai/tokens.go` |
| Add or change a Claude model           | `pkg/ai/anthropic/anthropic.go`        |
| Mark what a Claude model accepts       | `pkg/ai/anthropic/compat.go`           |
| Change the retry policy                | `pkg/ai/anthropic/retry.go`            |
| Build an Anthropic request             | `pkg/ai/anthropic/params.go`           |
| Convert messages for Anthropic         | `pkg/ai/anthropic/convert.go`          |
| Record a provider test                 | `pkg/ai/anthropic/anthropic_test.go`   |
| Generate an image or speech            | `pkg/ai/image.go`, `pkg/ai/speech.go`  |

## Related Concepts

- [Message](message.md)
- [Stream](stream.md)
