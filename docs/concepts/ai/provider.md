---
title: Provider
description: How a provider serves a model call, and what each capability of a provider asks.
package: pkg/ai
read_when:
  - You bind a model to a provider
  - You add an option that one provider reads
  - You write a provider
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
- Temperature, cache retention, session ID, headers, and metadata are gone, and a provider that needs one defines its own option. Caching is always on. Temperature returns a 400 on Claude Opus 5 and Sonnet 5.
- There is no tool choice option, and the model decides on each call if it calls a tool. In an agent, a choice for every call either blocks every tool call or forces a tool call on every turn, and a forced call never lets the loop end. No caller set a choice for one call. Claude Fable 5.1 also rejects a forced tool choice.
- A thinking level maps to an effort of the API. A model lists the levels that it takes, but the provider does not check the list. A pass-through model has no list, and the API rejects a level with a clear error. No rank resolves a level to the closest one that a model supports.
- A model holds its limits, its input kinds, its thinking levels, and its prices. Each provider package fills them from the docs of the API, so a caller can pick a model or show a price without a second table. A zero field is unknown, so a pass-through model works with only its ID.
- Each provider is a sub-package of ai, not its own module. A module per provider keeps the SDK dependencies apart. The Anthropic and OpenAI SDKs add few dependencies, so the split has no benefit.
- A provider sends the `ai.SystemPrompt` option as the system prompt of its API and puts the cache marker on it. The option takes a slice of blocks, so a provider reads it with `GetOption` like every other option. A variadic setter lost, because it cannot be the key of `GetOption` and needed a reader of its own.
- A token count sends the same messages, system prompt, tools, and thinking config as a call, so the count matches.
- The provider has no OAuth, no environment detection, and no logging. Each belongs to a later layer.

## Map

| Task                                   | File                                   |
| -------------------------------------- | -------------------------------------- |
| Bind a model to a provider             | `pkg/ai/text.go`                       |
| Add an option                          | `pkg/ai/options.go`                    |
| Add an optional capability             | `pkg/ai/object.go`, `pkg/ai/tokens.go` |
| Generate an image or speech            | `pkg/ai/image.go`, `pkg/ai/speech.go`  |

## Related Concepts

- [Message](message.md)
- [Stream](stream.md)
