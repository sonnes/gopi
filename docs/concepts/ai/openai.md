---
title: OpenAI Providers
description: How the two OpenAI providers build their requests, keep reasoning between calls, and serve compatible servers.
package: pkg/ai/openai, pkg/ai/openaichat
read_when:
  - You change a request that an OpenAI provider sends
  - You add a server that copies an OpenAI API
  - A reasoning model loses its reasoning between tool calls
  - You record an OpenAI provider test
---

# OpenAI Providers

OpenAI has two text APIs, and gopi has one provider for each. `openai` calls the Responses API. `openaichat` calls the Chat Completions API, which OpenRouter and Fireworks also serve.

```
                     +--> openai.Provider       Responses API
                     |      api.openai.com, OpenRouter through openrouter.Provider
ai.LanguageModel ----+
                     |
                     +--> openaichat.Provider   Chat Completions API
                            api.openai.com, OpenRouter, Fireworks through fireworks.Provider
```

## Design

- Each API has its own module, because the two APIs share no wire format, stream, or conversion.
- The Responses provider sets `store` to false and asks for the encrypted reasoning. gopi keeps the history in its messages, and OpenRouter rejects a stored response.
- A thinking block from `openai` holds the ID and the encrypted content of its reasoning item as JSON in `Signature`. The API needs both, and the block has one opaque field. On send, the provider drops a thinking block with a signature that does not decode, such as one from Anthropic.
- `openai` builds the final message from the output of the terminal event, because that output holds the encrypted content and the item order. The stream opens a block for each item in output order, so the index of an event matches the final message. A stream that ends before the terminal event fails.
- `openai` sends the system prompt as a developer message, because a cache breakpoint needs a content part. The implicit breakpoint of the API stays on, so the pair matches the Anthropic provider. Only the GPT-6 and GPT-5.6 models get the breakpoint, because an older model such as GPT-5.4 mini returns a 400 for it.
- In both providers, the long `ai.CacheRetention` sends `prompt_cache_retention` of 24 hours, and none sends no breakpoint and no cache key. Neither provider sends `prompt_cache_options`, because OpenRouter rejects it.
- On a known GPT model, a removal keeps the tool list and limits the model with an `allowed_tools` tool choice, so the cache holds. On Responses, an addition on that model goes in place as an `additional_tools` item for the same reason. The cost of every other change is in [Tool Change](../agent/tool-change.md).
- `openaichat` sends only the fields that OpenAI, OpenRouter, and Fireworks all accept, because Fireworks rejects an unknown field. The cache breakpoint is the `CacheBreakpoints` option, and the cache key and the long retention go only when the caller sets them.
- `openaichat` sends a thinking block back as `reasoning_content`. Fireworks needs it for a model with interleaved thinking, and OpenRouter reads it as its reasoning field. OpenAI sends no reasoning text on this API, so an OpenAI response has no thinking block.
- If no OpenAI key is set, the provider tests record from OpenRouter. The token count and image tests record only from api.openai.com, and the Fireworks test records only from Fireworks. Without a fixture, a test skips.

## Map

| Task                                  | File                                                                   |
| ------------------------------------- | ---------------------------------------------------------------------- |
| Build a Responses request             | `pkg/ai/openai/params.go`                                              |
| Convert messages to Responses items   | `pkg/ai/openai/convert.go`                                             |
| Read a Responses stream               | `pkg/ai/openai/openai.go`                                              |
| Generate an image                     | `pkg/ai/openai/image.go`                                               |
| Build a Chat Completions request      | `pkg/ai/openaichat/params.go`                                          |
| Convert messages for Chat Completions | `pkg/ai/openaichat/convert.go`                                         |
| Read a Chat Completions stream        | `pkg/ai/openaichat/openaichat.go`                                      |
| Mark what an OpenAI model accepts     | `pkg/ai/openai/compat.go`, `pkg/ai/openaichat/compat.go`               |
| Record a provider test                | `pkg/ai/openai/openai_test.go`, `pkg/ai/openaichat/openaichat_test.go` |

## Related Concepts

- [Provider](provider.md)
- [Host Provider](host-provider.md)
- [Message](message.md)
- [Stream](stream.md)
